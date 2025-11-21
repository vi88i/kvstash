// Package store implements the core storage engine for KVStash
// It provides thread-safe key-value storage backed by an append-only log file
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kvstash/constants"
	"kvstash/models"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Validation errors that should result in HTTP 400 responses
var (
	ErrEmptyKey      = errors.New("key should not be empty")
	ErrKeyTooLarge   = errors.New("key exceeds maximum size")
	ErrValueTooLarge = errors.New("value exceeds maximum size")
	ErrKeyNotFound   = errors.New("key not found in index")
)

// segmentFilePattern is used to find the segment files in directory
var segmentFilePattern = regexp.MustCompile(`^seg(\d+)\.log$`)

// Store manages the key-value storage with thread-safe access
// It maintains an in-memory index for fast lookups and uses a log writer for persistence
type Store struct {
	// index maps keys to their storage locations in the log file
	index models.KVStashIndex

	// writer handles appending new entries to the active log file
	writer *LogWriter

	// mu protects concurrent access to the index, activeLog, activeLogCount, segmentCount, and writer
	mu sync.RWMutex

	// dbPath is the directory where database files are stored
	dbPath string

	// segmentCount tracks the total number of segments (including active log)
	segmentCount int

	// activeLog tracks the active log file name
	activeLog string

	// activeLogCount tracks the number of writes to the active log (includes updates to existing keys)
	activeLogCount int
}

// segmentFile represents a numbered segment file in the database
type segmentFile struct {
	// name is the filename (e.g., "seg0.log", "seg1.log")
	name string

	// num is the segment number extracted from the filename
	num int
}

// NewStore creates and initializes a new Store instance
// It builds the index by reading all existing segment files and initializes the writer for the active log
// Creates the database directory if it doesn't exist
// Returns an error if the index cannot be built or the writer cannot be created
func NewStore(dbPath string) (*Store, error) {
	// Create database directory if it doesn't exist
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return nil, fmt.Errorf("NewStore: failed to create database directory: %w", err)
	}

	s := &Store{
		index:        make(models.KVStashIndex),
		dbPath:       dbPath,
		segmentCount: 0,
		activeLog:    "seg0.log",
	}

	if err := s.buildIndex(); err != nil {
		return nil, fmt.Errorf("NewStore: failed to build index: %w", err)
	}

	writer, err := newLogWriter(dbPath, s.activeLog)
	if err != nil {
		return nil, fmt.Errorf("NewStore: failed to create writer: %w", err)
	}
	s.writer = writer

	if dbPath == constants.DBPath {
		go s.autoCompact()
	}

	return s, nil
}

// Set stores a key-value pair in the store
// The operation is thread-safe and validates key/value size limits
// Automatically rotates to a new segment when the active log reaches MaxKeysPerSegment writes
// Returns validation errors (ErrEmptyKey, ErrKeyTooLarge, ErrValueTooLarge) for client errors
// Returns other errors for server-side failures
func (s *Store) Set(req *models.KVStashRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(req.Key) == 0 {
		return ErrEmptyKey
	}

	if len(req.Key) > constants.MaxKeySize {
		return fmt.Errorf("%w (%d bytes)", ErrKeyTooLarge, constants.MaxKeySize)
	}

	if len(req.Value) > constants.MaxValueSize {
		return fmt.Errorf("%w (%d bytes)", ErrValueTooLarge, constants.MaxValueSize)
	}

	if s.activeLogCount >= constants.MaxKeysPerSegment {
		if err := s.Close(); err != nil {
			return fmt.Errorf("Set: failed to close active log - %v: %w", s.activeLog, err)
		}

		activeLog := fmt.Sprintf("%v%v%v", constants.SegmentNamePrefix, s.segmentCount+1, constants.SegmentNameExt)
		writer, err := newLogWriter(s.dbPath, activeLog)
		if err != nil {
			return fmt.Errorf("Set: failed to create new active log - %v: %w", activeLog, err)
		}
		s.writer = writer
		s.activeLog = activeLog
		s.activeLogCount = 0
		s.segmentCount++
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("Set: failed to serialize: %w", err)
	}
	metadata, err := s.writer.Write(data)
	if err != nil {
		return fmt.Errorf("Set: %w", err)
	}

	s.index[req.Key] = &models.KVStashIndexEntry{
		SegmentFile: s.activeLog,
		Offset:      metadata.Offset,
		Size:        metadata.Size,
		Checksum:    metadata.Checksum,
	}
	s.activeLogCount++
	log.Printf("Set: Added key=%v in segment=%v/%v", req.Key, s.dbPath, s.activeLog)

	return nil
}

// Get retrieves the value for a given key from the store
// The operation is thread-safe using a read lock on the index
// If a checksum mismatch is detected, the corrupted entry is purged from the index
// Returns ErrKeyNotFound for missing keys (client error)
// Returns other errors for server-side failures
func (s *Store) Get(req *models.KVStashRequest) (string, error) {
	s.mu.RLock()
	entry, ok := s.index[req.Key]
	s.mu.RUnlock()

	if !ok {
		return "", ErrKeyNotFound
	}

	value, err := fetchValue(s.dbPath, entry.SegmentFile, entry.Offset, entry.Size, entry.Checksum)
	if err != nil {
		// Check if this is a checksum mismatch error
		if errors.Is(err, ErrChecksumMismatch) {
			// Purge the corrupted entry from the index
			s.mu.Lock()
			delete(s.index, req.Key)
			s.mu.Unlock()
			log.Printf("Get: purged corrupted entry for key=%v due to checksum mismatch", req.Key)
		}
		return "", fmt.Errorf("Get: %w", err)
	}

	return value, nil
}

// buildIndex reconstructs the in-memory index by scanning all segment files
// It reads all entries, validates metadata checksums only, and populates the index
// Tolerates corruption in the active log but fails on corruption in archived segments
// Attempts recovery from backup if database is missing but backup exists
// Returns an error if segment files cannot be opened or read
func (s *Store) buildIndex() error {
	// Check if backup exists and database doesn't - recovery scenario
	if _, err := os.Stat(s.dbPath); os.IsNotExist(err) {
		if _, backupErr := os.Stat(constants.BackupDBPath); backupErr == nil {
			log.Printf("buildIndex: database missing but backup exists, attempting recovery")
			if err := copyDB(constants.BackupDBPath, s.dbPath); err != nil {
				panic(fmt.Sprintf("buildIndex: failed to restore from backup: %v", err))
			}
			if err := os.RemoveAll(constants.BackupDBPath); err != nil {
				log.Printf("buildIndex: failed to delete backup after recovery: %v", err)
			}
			log.Printf("buildIndex: successfully recovered from backup")
		}
	}

	segments, err := s.getSegmentFiles()
	if err != nil {
		return fmt.Errorf("buildIndex: failed fetch segment files: %w", err)
	}

	for _, segment := range segments {
		file, err := os.OpenFile(filepath.Join(s.dbPath, segment), os.O_RDONLY, 0644)
		if err != nil {
			return fmt.Errorf("buildIndex: failed to open file: %w", err)
		}

		if err := s.readSegment(file, segment); err != nil {
			// don't tolerate checksum corruption in non-active log
			if segment != s.activeLog {
				s.index = make(models.KVStashIndex)
				file.Close()
				return fmt.Errorf("buildIndex: non-active log corrupted - %v: %w", segment, err)
			}

			log.Printf("buildIndex: %v", err)
		}
		file.Close()
	}

	return nil
}

// Close closes the store and releases resources
func (s *Store) Close() error {
	if s.writer != nil {
		err := s.writer.Close()
		if err == nil {
			s.writer = nil
		}
		return err
	}
	return nil
}

// getSegmentFiles scans the database directory and returns an ordered list of segment files
// It returns segment files sorted by their numeric suffix (seg0.log, seg1.log, ...)
// Also determines and sets the active log filename based on existing segments
// This ensures entries are read in chronological order during index building
func (s *Store) getSegmentFiles() ([]string, error) {
	dbDirPath := filepath.Join(s.dbPath)
	entries, err := os.ReadDir(dbDirPath)
	if err != nil {
		return nil, fmt.Errorf("getSegmentFiles: failed to read directory %v: %w", dbDirPath, err)
	}

	segments := []segmentFile{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !segmentFilePattern.MatchString(name) {
			continue
		}

		numStr := name[len(constants.SegmentNamePrefix) : len(name)-len(constants.SegmentNameExt)]
		num, err := strconv.ParseUint(numStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("getSegmentFiles: invalid segment number: %w", err)
		}

		segments = append(segments, segmentFile{name, int(num)})
	}

	sort.Slice(segments, func(i, j int) bool {
		return segments[i].num < segments[j].num
	})

	matches := make([]string, 0, len(segments))
	for i := range segments {
		matches = append(matches, segments[i].name)
	}

	noOfSegments := len(matches)
	if noOfSegments > 0 {
		s.segmentCount = noOfSegments
		s.activeLog = fmt.Sprintf("%v%v%v", constants.SegmentNamePrefix, noOfSegments-1, constants.SegmentNameExt)
	} else {
		s.activeLog = fmt.Sprintf("%v0%v", constants.SegmentNamePrefix, constants.SegmentNameExt)
	}

	return matches, nil
}

// readSegment reads all entries from a segment file and populates the index
// It validates metadata checksums and returns an error on the first corrupted entry
// If reading the active log, it also increments activeLogCount for each entry found
// Returns an error if the file cannot be read or contains invalid data
func (s *Store) readSegment(file *os.File, segment string) error {
	if file == nil {
		return fmt.Errorf("readSegment: nil file %v", segment)
	}

	buf := make([]byte, constants.MetadataSize)
	for {
		// read metadata
		n, err := file.Read(buf)

		// check for EOF
		if err == io.EOF {
			if n == 0 {
				// clean EOF
				return nil
			}

			// if n > 0
			return fmt.Errorf("readSegment: truncated metadata")
		}

		if err != nil {
			return fmt.Errorf("readSegment: failed to read metadata: %w", err)
		}

		// ensure we read exactly MetadataSize bytes
		if n != constants.MetadataSize {
			return fmt.Errorf("readSegment: truncated metadata")
		}

		// Deserialize metadata
		var metadata models.KVStashMetadata
		if err := metadata.Deserialize(buf); err != nil {
			return fmt.Errorf("readSegment: failed to deserialize metadata: %w", err)
		}

		// Validate metadata checksum
		if metadata.ValidateMChecksum() != nil {
			return fmt.Errorf("readSegment: metadata checksum failed")
		}

		// Read value data
		dataBytes := make([]byte, metadata.Size)
		n, err = file.Read(dataBytes)
		if err != nil && err != io.EOF {
			return fmt.Errorf("readSegment: failed to read value data: %w", err)
		}

		// Check if we've read the exact amount of bytes
		if int64(n) != metadata.Size {
			return fmt.Errorf("readSegment: incomplete value read (%d bytes), expected %d", n, metadata.Size)
		}

		// Deserialize value
		var data models.KVStashRequest
		if err := json.Unmarshal(dataBytes, &data); err != nil {
			return fmt.Errorf("readSegment: failed to deserialize value: %w", err)
		}

		log.Printf("readSegment: read key=%v", data.Key)
		s.index[data.Key] = &models.KVStashIndexEntry{
			SegmentFile: segment,
			Offset:      metadata.Offset,
			Size:        metadata.Size,
			Checksum:    metadata.Checksum,
		}

		if s.activeLog == segment {
			s.activeLogCount++
		}
	}
}

// autoCompact runs periodic compaction to reclaim disk space and optimize storage
// This goroutine is automatically started only for the main database store (not for temporary stores)
//
// Compaction Process:
//  1. Creates a backup of the current database to BackupDBPath
//  2. Creates a new store at TmpDBPath
//  3. Copies all current key-value pairs from the old store to the new store
//     (this eliminates old values for updated keys and defragments the data)
//  4. Attempts to replace the old database with the compacted one:
//     - Closes the old store writer
//     - Deletes the old database directory
//     - Renames TmpDBPath to DBPath
//  5. On success: Updates store references and cleans up backup
//  6. On failure: Recovers from backup and panics if recovery fails
//
// Lock Strategy:
// The store mutex (oldStore.mu) is held for the entire compaction cycle to prevent
// concurrent reads/writes during the database swap operation. This ensures data consistency
// but blocks all Get/Set operations during compaction.
//
// Error Handling:
// - Backup creation failure: Skip this compaction cycle and retry next interval
// - New store creation failure: Skip this compaction cycle and retry next interval
// - Data copy failure: Clean up resources (newStore, TmpDBPath, BackupDBPath) and retry next cycle
// - Database swap failure: Attempt recovery from backup, panic if recovery fails
// - Recovery failure: Panic (database is in inconsistent state, cannot continue safely)
//
// Resource Cleanup:
// - On success: BackupDBPath is removed
// - On copy failure: newStore, TmpDBPath, and BackupDBPath are cleaned up
// - On swap failure with successful recovery: TmpDBPath is removed, backup is restored
//
// This function runs indefinitely in a loop with CompactionInterval second delays between cycles.
func (oldStore *Store) autoCompact() {
	for {
		time.Sleep(time.Second * constants.CompactionInterval)

		oldStore.mu.Lock()
		// Step 1: Create backup before any modifications
		if err := copyDB(constants.DBPath, constants.BackupDBPath); err != nil {
			log.Printf("autoCompact: backup failed: %v", err)
			oldStore.mu.Unlock()
			continue
		}

		// Step 2: Create new store at temporary location
		// Note: NewStore will NOT spawn autoCompact goroutine because dbPath != constants.DBPath
		newStore, err := NewStore(constants.TmpDBPath)
		if err != nil {
			log.Printf("autoCompact: creating new store failed: %v", err)
			oldStore.mu.Unlock()
			continue
		}

		// Step 3: Group keys by segment file for efficient reading
		// This allows us to read from each segment file sequentially
		var keysGroupedBySegments map[string][]string = make(map[string][]string)
		for key, entry := range oldStore.index {
			segment := entry.SegmentFile
			_, ok := keysGroupedBySegments[segment]
			if !ok {
				keysGroupedBySegments[segment] = make([]string, 0)
			}

			keysGroupedBySegments[segment] = append(keysGroupedBySegments[segment], key)
		}

		copySuccess := true

		// Step 4: Copy all current key-value pairs to the new store
	compactLoop:
		for _, keys := range keysGroupedBySegments {
			noOfKeys := len(keys)
			for i := range noOfKeys {
				key := keys[i]

				entry := oldStore.index[key]
				// Fetch the current value from the old store
				value, err := fetchValue(oldStore.dbPath, entry.SegmentFile, entry.Offset, entry.Size, entry.Checksum)
				if err != nil {
					log.Printf("autoCompact: failed to fetch %v: %v", key, err)
					copySuccess = false
					break compactLoop
				}

				// Write the key-value pair to the new store
				req := &models.KVStashRequest{
					Key:   key,
					Value: value,
				}
				if err := newStore.Set(req); err != nil {
					log.Printf("autoCompact: failed to set key in new store %v: %v", key, err)
					copySuccess = false
					break compactLoop
				}
			}
		}

		if copySuccess {
			recover := false

			// Close old store writer to release file handles
			if err := oldStore.Close(); err != nil {
				log.Printf("autoCompact: failed to close old store writer: %v", err)
				recover = true
			}

			// Close new store writer before rename (Windows requires this)
			if err := newStore.Close(); err != nil {
				log.Printf("autoCompact: failed to close new store writer: %v", err)
				recover = true
			}

			// Remove old database directory
			if err := os.RemoveAll(constants.DBPath); err != nil {
				log.Printf("autoCompact: failed delete old store: %v", err)
				recover = true
			}

			// Rename tmp database to main database location
			if err := os.Rename(constants.TmpDBPath, constants.DBPath); err != nil {
				log.Printf("autoCompact: failed to rename tmp db: %v", err)
				recover = true
			}

			if recover {
				// Clean up temporary database directory
				if err := os.RemoveAll(constants.TmpDBPath); err != nil {
					log.Printf("autoCompact: failed to remove tmp db: %v", err)
				}

				// Copy backup DB back to active DB
				if err := copyDB(constants.BackupDBPath, constants.DBPath); err != nil {
					panic(err)
				}

				// Recreate writer for the restored database
				writer, err := newLogWriter(constants.DBPath, oldStore.activeLog)
				if err != nil {
					panic(err)
				}
				oldStore.writer = writer
			} else {
				// Success path - rename succeeded, newStore is now at DBPath
				// Reopen the writer at the new location
				writer, err := newLogWriter(constants.DBPath, newStore.activeLog)
				if err != nil {
					log.Printf("autoCompact: failed to reopen writer after rename: %v", err)
					// Try to recover from backup
					if err := copyDB(constants.BackupDBPath, constants.DBPath); err != nil {
						panic(err)
					}
					writer, err = newLogWriter(constants.DBPath, oldStore.activeLog)
					if err != nil {
						panic(err)
					}
					oldStore.writer = writer
				} else {
					// Successfully reopened writer, update store references
					oldStore.index = newStore.index
					oldStore.activeLog = newStore.activeLog
					oldStore.activeLogCount = newStore.activeLogCount
					oldStore.segmentCount = newStore.segmentCount
					oldStore.writer = writer

					// Clean up backup after successful compaction
					if err := os.RemoveAll(constants.BackupDBPath); err != nil {
						log.Printf("autoCompact: failed to delete backup: %v", err)
					}
				}
			}
		} else {
			if err := newStore.Close(); err != nil {
				log.Printf("autoCompact: failed to close new store writer: %v", err)
			}

			if err := os.RemoveAll(constants.BackupDBPath); err != nil {
				log.Printf("autoCompact: failed delete - %v: %v", constants.BackupDBPath, err)
			}

			if err := os.RemoveAll(constants.TmpDBPath); err != nil {
				log.Printf("autoCompact: failed to delete - %v: %v", constants.TmpDBPath, err)
			}

			log.Printf("autoCompact: skipping store replacement")
		}

		oldStore.mu.Unlock()
	}
}

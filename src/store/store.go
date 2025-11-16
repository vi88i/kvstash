// Package store implements the core storage engine for KVStash
// It provides thread-safe key-value storage backed by an append-only log file
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kvstash/src/constants"
	"kvstash/src/models"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
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

	// mu protects concurrent access to the index
	mu sync.RWMutex

	// dbPath is the directory where database files are stored
	dbPath string

	// segmentCount tracks the number of archived segments present
	segmentCount int
}

// segmentFile represents a numbered segment file in the database
type segmentFile struct {
	// name is the filename (e.g., "seg0.log", "seg1.log")
	name string

	// num is the segment number extracted from the filename
	num int
}

// NewStore creates and initializes a new Store instance
// It builds the index by reading the existing log file and initializes the writer
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
	}

	if err := s.buildIndex(); err != nil {
		return nil, fmt.Errorf("NewStore: failed to build index: %w", err)
	}

	writer, err := newLogWriter(dbPath)
	if err != nil {
		return nil, fmt.Errorf("NewStore: failed to create writer: %w", err)
	}
	s.writer = writer

	return s, nil
}

// Set stores a key-value pair in the store
// The operation is thread-safe and validates key/value size limits
// Returns validation errors (ErrEmptyKey, ErrKeyTooLarge, ErrValueTooLarge) for client errors
// Returns other errors for server-side failures
func (s *Store) Set(req *models.KVStashRequest) error {
	if len(req.Key) == 0 {
		return ErrEmptyKey
	}

	if len(req.Key) > constants.MaxKeySize {
		return fmt.Errorf("%w (%d bytes)", ErrKeyTooLarge, constants.MaxKeySize)
	}

	if len(req.Value) > constants.MaxValueSize {
		return fmt.Errorf("%w (%d bytes)", ErrValueTooLarge, constants.MaxValueSize)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("Set: failed to serialize: %w", err)
	}
	metadata, err := s.writer.Write(data)
	if err != nil {
		return fmt.Errorf("Set: %w", err)
	}

	s.mu.Lock()
	s.index[req.Key] = &models.KVStashIndexEntry{
		SegmentFile: constants.ActiveLogFileName,
		Offset:      metadata.Offset,
		Size:        metadata.Size,
		Checksum:    metadata.Checksum,
	}
	s.mu.Unlock()

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

// buildIndex reconstructs the in-memory index by scanning the active log file
// It reads all entries, validates their checksums, and populates the index
// Returns an error if the file cannot be opened or read
func (s *Store) buildIndex() error {
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
			if segment != constants.ActiveLogFileName {
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
		return s.writer.Close()
	}
	return nil
}

// getSegmentFiles scans the database directory and returns an ordered list of segment files
// It returns segment files sorted by their numeric suffix (seg0.log, seg1.log, ...)
// followed by the active log file. This ensures entries are read in chronological order.
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

	matches := make([]string, 0, len(segments)+1)
	for i := range segments {
		matches = append(matches, segments[i].name)
	}
	s.segmentCount = len(matches)
	matches = append(matches, constants.ActiveLogFileName)

	return matches, nil
}

// readSegment reads all entries from a segment file and populates the index
// It validates metadata checksums and stops on the first corrupted entry
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
	}
}

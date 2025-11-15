// Package store implements the core storage engine for KVStash
// It provides thread-safe key-value storage backed by an append-only log file
package store

import (
	"encoding/json"
	"fmt"
	"io"
	"kvstash/src/constants"
	"kvstash/src/models"
	"log"
	"os"
	"path/filepath"
	"sync"
)

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
}

// NewStore creates and initializes a new Store instance
// It builds the index by reading the existing log file and initializes the writer
// Returns an error if the index cannot be built or the writer cannot be created
func NewStore(dbPath string) (*Store, error) {
	s := &Store{
		index:  make(models.KVStashIndex),
		dbPath: dbPath,
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
// Returns an error if validation fails or the write operation fails
func (s *Store) Set(req *models.KVStashRequest) error {
	if len(req.Key) == 0 {
		return fmt.Errorf("Set: key should not be empty")
	}

	if len(req.Key) > constants.MaxKeySize {
		return fmt.Errorf("Set: key exceeds maximum size of %d bytes", constants.MaxKeySize)
	}

	if len(req.Value) > constants.MaxValueSize {
		return fmt.Errorf("Set: value exceeds maximum size of %d bytes", constants.MaxValueSize)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("Set: failed to serialize: %w", err)
	}
	offset, size, err := s.writer.Write(data)
	if err != nil {
		return fmt.Errorf("Set: %w", err)
	}

	s.mu.Lock()
	s.index[req.Key] = &models.KVStashIndexEntry{
		SegmentFile: constants.ActiveLogFileName,
		Offset:      int64(offset),
		Size:        int64(size),
	}
	s.mu.Unlock()

	return nil
}

// Get retrieves the value for a given key from the store
// The operation is thread-safe using a read lock on the index
// Returns an error if the key is not found or the read operation fails
func (s *Store) Get(req *models.KVStashRequest) (string, error) {
	s.mu.RLock()
	entry, ok := s.index[req.Key]
	s.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("Get: key not found in index")
	}

	value, err := fetchValue(s.dbPath, entry.SegmentFile, entry.Offset, entry.Size)
	if err != nil {
		return "", fmt.Errorf("Get: %w", err)
	}

	return value, nil
}

// buildIndex reconstructs the in-memory index by scanning the active log file
// It reads all entries, validates their checksums, and populates the index
// Returns an error if the file cannot be opened or read
func (s *Store) buildIndex() error {
	file, err := os.OpenFile(filepath.Join(s.dbPath, constants.ActiveLogFileName), os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return fmt.Errorf("buildIndex: failed to open file: %w", err)
	}
	defer file.Close()

	buf := make([]byte, constants.MetadataSize)
	for {
		// read metadata
		n, err := file.Read(buf)

		// check for EOF
		if err == io.EOF {
			if n == 0 {
				// clean EOF
				break
			}

			// if n > 0
			log.Println("buildIndex: truncated metadata")
		}

		if err != nil {
			log.Printf("buildIndex: failed to read metadata: %v", err)
			break
		}

		// ensure we read exactly MetadataSize bytes
		if n != constants.MetadataSize {
			log.Println("buildIndex: truncated metadata")
			break
		}

		// Deserialize metadata
		var metadata models.KVStashMetadata
		if err := metadata.Deserialize(buf); err != nil {
			log.Printf("buildIndex: failed to deserialize metadata: %v", err)
			break
		}

		// Validate metadata checksum
		if metadata.ValidateMChecksum() != nil {
			log.Println("buildIndex: metadata checksum failed")
			break
		}

		// Read value data
		dataBytes := make([]byte, metadata.Size)
		n, err = file.Read(dataBytes)
		if err != nil && err != io.EOF {
			log.Printf("buildIndex: failed to read value data: %v", err)
			break
		}

		// Check if we've read the exact amount of bytes
		if int64(n) != metadata.Size {
			log.Printf("buildIndex: incomplete value read (%d bytes), expected %d", n, metadata.Size)
			break
		}

		// Deserialize value
		var data models.KVStashRequest
		if err := json.Unmarshal(dataBytes, &data); err != nil {
			log.Printf("buildIndex: failed to deserialize value: %v", err)
			break
		}

		log.Printf("buildIndex: read key=%v", data.Key)
		s.index[data.Key] = &models.KVStashIndexEntry{
			SegmentFile: constants.ActiveLogFileName,
			Offset:      metadata.Offset,
			Size:        metadata.Size,
		}
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

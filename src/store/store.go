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
	"sync"
)

// Validation errors that should result in HTTP 400 responses
var (
	ErrEmptyKey      = errors.New("key should not be empty")
	ErrKeyTooLarge   = errors.New("key exceeds maximum size")
	ErrValueTooLarge = errors.New("value exceeds maximum size")
	ErrKeyNotFound   = errors.New("key not found in index")
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
// Creates the database directory if it doesn't exist
// Returns an error if the index cannot be built or the writer cannot be created
func NewStore(dbPath string) (*Store, error) {
	// Create database directory if it doesn't exist
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return nil, fmt.Errorf("NewStore: failed to create database directory: %w", err)
	}

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
			break
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
			Checksum:    metadata.Checksum,
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

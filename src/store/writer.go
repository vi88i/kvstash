package store

import (
	"fmt"
	"kvstash/constants"
	"kvstash/models"
	"log"
	"os"
	"path/filepath"
	"sync"
)

/*
Log Writer Design Notes:

Requirements:
- Multiple writers
- Multiple readers
- Durability vs Throughput trade-off

1. Durability vs Throughput:
   When opened with O_SYNC, file writes are synchronous (high durability, lower throughput)
   Without O_SYNC, kernel batches writes (higher throughput, lower durability)

2. Thread Safety:
   Mutex protects concurrent writes from multiple goroutines
*/

// LogWriter handles thread-safe append operations to the active log file
// It maintains the current offset and ensures synchronous writes for durability
type LogWriter struct {
	// file is the open file handle for the active log file
	file *os.File

	// offset tracks the current write position in the file
	offset int64

	// mu protects concurrent write operations
	mu sync.Mutex

	// name is the log filename used for checksum computation
	name string
}

// newLogWriter creates a new LogWriter for the specified database path and log file
// Opens the file with O_CREATE|O_SYNC|O_WRONLY for synchronous I/O (durability over throughput)
// If the file already exists, it resumes writing from the current end of file
// Returns an error if the file cannot be opened or queried
func newLogWriter(dbPath string, activeLog string) (*LogWriter, error) {
	logPath := filepath.Join(dbPath, activeLog)

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_SYNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("newLogWriter: failed to open file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("newLogWriter: failed to stat file: %w", err)
	}

	return &LogWriter{file: file, offset: info.Size(), name: activeLog}, nil
}

// Write appends data to the log file with metadata and checksums
// The write format is: [metadata (112 bytes)][value data]
// Automatically rolls back the offset on partial write failures
// Returns the metadata containing offset, size, and checksums
// Thread-safe: uses mutex to serialize concurrent writes
func (lw *LogWriter) Write(data []byte, flags []int64) (*models.KVStashMetadata, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	metadataFlag := models.ComputeMetadataFlag(flags)
	metaDataOffset := lw.offset
	valueOffset := metaDataOffset + constants.MetadataSize
	valueSize := int64(len(data))
	metadata := models.KVStashMetadata{}
	if err := metadata.ComputeChecksum(valueOffset, valueSize, metadataFlag, lw.name, data); err != nil {
		return &metadata, fmt.Errorf("Write: metadata compute failed: %w", err)
	}

	n, err := lw.file.WriteAt(metadata.Serialize(), metaDataOffset)
	if err != nil {
		return &metadata, fmt.Errorf("Write: metadata write failed: %w", err)
	}

	if n != constants.MetadataSize {
		log.Printf("Write: expected size: %v, recvd size: %v", constants.MetadataSize, n)
		return &metadata, fmt.Errorf("Write: metadata size inconsistent")
	}

	lw.offset += constants.MetadataSize
	n, err = lw.file.WriteAt(data, valueOffset)
	bytesWritten := int64(n)
	if err != nil || bytesWritten != metadata.Size {
		lw.offset -= constants.MetadataSize
		return &metadata, fmt.Errorf("Write: value write failed: %w", err)
	}
	lw.offset += int64(n)

	return &metadata, nil
}

// Close closes the log file and releases the file handle
// Returns an error if the close operation fails
func (lw *LogWriter) Close() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if err := lw.file.Close(); err != nil {
		return fmt.Errorf("Close: failed to close file: %w", err)
	}

	return nil
}

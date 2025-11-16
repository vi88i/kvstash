package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kvstash/models"
	"os"
	"path/filepath"
)

// ErrChecksumMismatch indicates that the stored data does not match its checksum
// This suggests data corruption and the entry should be purged from the index
var ErrChecksumMismatch = errors.New("checksum mismatch: data corrupted")

// fetchValue reads a value from the log file at the specified offset and size
// It validates inputs, reads the exact bytes, and deserializes the JSON data
// Returns the value string or an error if validation or read fails
// Returns ErrChecksumMismatch if the data checksum doesn't match the stored checksum
func fetchValue(dbPath string, fileName string, offset int64, size int64, checksum [32]byte) (string, error) {
	// Validate inputs
	if size <= 0 {
		return "", fmt.Errorf("fetchValue: size must be positive, got %d", size)
	}

	if offset < 0 {
		return "", fmt.Errorf("fetchValue: offset must be non-negative, got %d", offset)
	}

	// Construct full file path
	filePath := filepath.Join(dbPath, fileName)

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("fetchValue: failed to open file %s: %w", fileName, err)
	}
	defer file.Close()

	// Get file size to validate offset
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("fetchValue: failed to stat file: %w", err)
	}

	if offset+int64(size) > fileInfo.Size() {
		return "", fmt.Errorf("fetchValue: offset+size (%d+%d=%d) exceeds file size (%d)",
			offset, size, offset+int64(size), fileInfo.Size())
	}

	// Read the exact bytes at offset
	buf := make([]byte, size)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("fetchValue: failed to read at offset %d: %w", offset, err)
	}

	if int64(n) != size {
		return "", fmt.Errorf("fetchValue: expected to read %d bytes, got %d", size, n)
	}

	var data models.KVStashRequest
	if err := json.Unmarshal(buf, &data); err != nil {
		return "", fmt.Errorf("fetchValue: failed to deserialize data - %w", err)
	}

	// Validate data integrity by recomputing and comparing checksums
	var metadata models.KVStashMetadata
	metadata.ComputeChecksum(offset, size, fileName, buf)
	if metadata.Checksum != checksum {
		return "", fmt.Errorf("fetchValue: %w (expected %x, got %x)",
			ErrChecksumMismatch, checksum, metadata.Checksum)
	}

	return data.Value, nil
}

package store

import (
	"encoding/json"
	"fmt"
	"io"
	"kvstash/src/models"
	"os"
	"path/filepath"
)

func fetchValue(dbPath string, fileName string, offset int64, size int64) (string, error) {
	// Validate inputs
	if size <= 0 {
		return "", fmt.Errorf("fetchValue: size must be positive, got %d", size)
	}

	if offset < 0 {
		return "", fmt.Errorf("fetchValue: offset must be non-negative, got %d", offset)
	}

	// Construct full file path
	filePath := filepath.Join(dbPath, fileName)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("fetchValue: file does not exist: %s", filePath)
	}

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

	return data.Value, nil
}

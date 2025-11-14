package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func fetchValue(dbPath string, fileName string, offset int64, size int) (string, error) {
	// Validate inputs
	if size <= 0 {
		return "", fmt.Errorf("size must be positive, got %d", size)
	}

	if offset < 0 {
		return "", fmt.Errorf("offset must be non-negative, got %d", offset)
	}

	// Construct full file path
	filePath := filepath.Join(dbPath, fileName)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist: %s", filePath)
	}

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", fileName, err)
	}
	defer file.Close()

	// Get file size to validate offset
	fileInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat file: %w", err)
	}

	if offset+int64(size) > fileInfo.Size() {
		return "", fmt.Errorf("offset+size (%d+%d=%d) exceeds file size (%d)",
			offset, size, offset+int64(size), fileInfo.Size())
	}

	// Read the exact bytes at offset
	buf := make([]byte, size)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read at offset %d: %w", offset, err)
	}

	if n != size {
		return "", fmt.Errorf("expected to read %d bytes, got %d", size, n)
	}

	return string(buf), nil
}

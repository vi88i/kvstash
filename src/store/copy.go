// Package store implements database copy operations for backup and compaction
package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copySegment copies a single segment file from source to destination
// It creates the destination file and uses io.Copy for efficient data transfer
// The destination file is synced to disk to ensure durability
// Returns an error if the source cannot be opened, destination cannot be created,
// copy fails, or sync fails
func copySegment(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	if err != nil {
		return err
	}

	if err := destination.Sync(); err != nil {
		return err
	}

	return nil
}

// copyDB copies an entire database directory from source to destination
// Used for creating backups before compaction and restoring from backups on recovery
//
// The operation performs the following steps:
// 1. Removes the destination directory if it exists (ensures clean state)
// 2. Creates the destination directory with 0755 permissions
// 3. Scans the source directory for segment files matching the pattern (seg*.log)
// 4. Copies each segment file using copySegment
//
// Only segment files matching segmentFilePattern are copied - directories and
// other files are skipped. This ensures only valid database files are copied.
//
// Returns an error if:
// - Destination directory cannot be removed or created
// - Source directory cannot be read
// - Any segment file copy fails
//
// Note: This function is atomic at the file level but not at the directory level.
// If a copy fails mid-operation, the destination may be left in a partial state.
func copyDB(source, destination string) error {
	// Remove destination directory to ensure clean state
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("copyDB: failed to delete destination directory - %v: %w", destination, err)
	}

	// Create fresh destination directory
	if err := os.MkdirAll(destination, 0755); err != nil {
		return fmt.Errorf("copyDB: failed to create destination directory - %v: %w", destination, err)
	}

	// Read source directory contents
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}

	// Copy only segment files (skip directories and non-segment files)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !segmentFilePattern.MatchString(name) {
			continue
		}

		if err := copySegment(filepath.Join(source, name), filepath.Join(destination, name)); err != nil {
			return err
		}
	}

	return nil
}

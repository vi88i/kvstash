package models

// KVStashIndexEntry represents metadata for locating a value in the log file
// Uses a soft-delete approach where deleted entries remain in the index but are marked
type KVStashIndexEntry struct {
	// SegmentFile is the name of the log file containing the value or tombstone
	SegmentFile string

	// Offset is the byte position in the file where the value data starts
	Offset int64

	// Size is the length in bytes of the value data (or tombstone data for deleted keys)
	Size int64

	// Deleted indicates if this entry represents a soft-deleted key (tombstone)
	// When true, Get operations return ErrKeyNotFound and compaction skips this entry
	// This allows the index to track deleted keys until compaction removes them physically
	Deleted bool

	// Checksum holds the SHA-256 checksum of the entry (value or tombstone)
	Checksum [32]byte
}

// KVStashIndex is a map from keys to their storage locations
// It enables O(1) lookups without scanning the entire log file
type KVStashIndex = map[string]*KVStashIndexEntry

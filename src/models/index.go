package models

// KVStashIndexEntry represents metadata for locating a value in the log file
type KVStashIndexEntry struct {
	// SegmentFile is the name of the log file containing the value
	SegmentFile string

	// Offset is the byte position in the file where the value data starts
	Offset int64

	// Size is the length in bytes of the value data
	Size int64

	// Checksum holds the SHA-256 checksum of the entry
	Checksum [32]byte
}

// KVStashIndex is a map from keys to their storage locations
// It enables O(1) lookups without scanning the entire log file
type KVStashIndex = map[string]*KVStashIndexEntry

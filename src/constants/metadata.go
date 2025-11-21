// Package constants defines application-wide configuration values and limits
package constants

const (
	// MetadataSize is the fixed size in bytes for metadata entries in the log file
	// Layout: 8 bytes (offset) + 8 bytes (size) + 32 bytes (segment file) + 32 bytes (checksum) + 32 bytes (metadata checksum) + 8 bytes (flags) = 120 bytes
	MetadataSize = 120

	// MaxKeySize is the maximum allowed size in bytes for a key
	MaxKeySize = 256 // 256 bytes

	// MaxValueSize is the maximum allowed size in bytes for a value
	MaxValueSize = 1048576 // 1 MB
)

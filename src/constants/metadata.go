// Package constants defines application-wide configuration values and limits
package constants

const (
	// MetadataSize is the fixed size in bytes for metadata entries in the log file
	// Layout: 8 bytes (offset) + 8 bytes (size) + 32 bytes (segment file) + 32 bytes (checksum) + 32 bytes (metadata checksum) = 112 bytes
	MetadataSize = 112

	// DBPath is the directory path where database files are stored
	DBPath = "db"

	// ActiveLogFileName is the name of the active log file where new entries are appended
	ActiveLogFileName = "active.log"

	// MaxKeySize is the maximum allowed size in bytes for a key
	MaxKeySize = 256 // 256 bytes

	// MaxValueSize is the maximum allowed size in bytes for a value
	MaxValueSize = 1048576 // 1 MB
)

package constants

const (
	// MaxKeysPerSegment is the maximum allowed keys per segment file
	MaxKeysPerSegment = 512

	// SegmentNamePrefix is the prefix of segment files
	SegmentNamePrefix = "seg"

	// SegmentNameExt is the extension of the segment files
	SegmentNameExt = ".log"

	// Compaction interval in seconds
	CompactionInterval = 60
)

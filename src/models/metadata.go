package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"kvstash/constants"
)

// KVStashMetadata represents the metadata for a log entry
// It contains information needed to locate and validate stored values
type KVStashMetadata struct {
	// Offset is the byte position in the file where the value data starts
	Offset int64

	// Size is the length in bytes of the value data
	Size int64

	// Flags is the tombstone indicator (during compaction delete from the logs)
	Flags int64

	// SegmentFile is the name of the log file (fixed 32-byte array)
	SegmentFile [32]byte

	// Checksum is the SHA-256 hash of the value data for integrity verification
	Checksum [32]byte

	// MChecksum is the SHA-256 hash of the metadata itself for integrity verification
	MChecksum [32]byte
}

// ComputeChecksum calculates and sets both the value checksum and metadata checksum
// It uses BigEndian encoding (network standard) for all fields
//
// The value checksum is SHA-256(offset || size || fileName || data || isDeleted)
// The metadata checksum is SHA-256(offset || size || fileName || isDeleted || valueChecksum)
func (m *KVStashMetadata) ComputeChecksum(offset int64, size int64, flags int64, fileName string, data []byte) error {
	fileNameBytes, err := fitFileName(fileName)
	if err != nil {
		return fmt.Errorf("ComputeChecksum: %w", err)
	}

	var buf1, buf2 bytes.Buffer

	// Compute value checksum: SHA-256(offset || size || fileName || data)
	if err := binary.Write(&buf1, binary.BigEndian, offset); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write offset: %w", err)
	}
	if err := binary.Write(&buf1, binary.BigEndian, size); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write size: %w", err)
	}
	if err := binary.Write(&buf1, binary.BigEndian, flags); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write flags: %w", err)
	}
	if err := binary.Write(&buf1, binary.BigEndian, fileNameBytes); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write fileName: %w", err)
	}
	if err := binary.Write(&buf1, binary.BigEndian, data); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write data: %w", err)
	}
	valueChecksum := sha256.Sum256(buf1.Bytes())

	// Compute metadata checksum: SHA-256(offset || size || fileName || valueChecksum)
	if err := binary.Write(&buf2, binary.BigEndian, offset); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write offset for metadata: %w", err)
	}
	if err := binary.Write(&buf2, binary.BigEndian, size); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write size for metadata: %w", err)
	}
	if err := binary.Write(&buf2, binary.BigEndian, flags); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write flags: %w", err)
	}
	if err := binary.Write(&buf2, binary.BigEndian, fileNameBytes); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write fileName for metadata: %w", err)
	}
	if err := binary.Write(&buf2, binary.BigEndian, valueChecksum); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write valueChecksum: %w", err)
	}
	metadataChecksum := sha256.Sum256(buf2.Bytes())

	m.Offset = offset
	m.Size = size
	m.Flags = flags
	m.SegmentFile = fileNameBytes
	m.Checksum = valueChecksum
	m.MChecksum = metadataChecksum
	return nil
}

// Serialize converts the metadata to a fixed-size byte array for storage
// Returns a 112-byte array in the following format:
//   - Bytes 0-7: Offset (8 bytes, BigEndian uint64)
//   - Bytes 8-15: Size (8 bytes, BigEndian uint64)
//   - Bytes 16-47: SegmentFile (32 bytes)
//   - Bytes 48-79: Checksum (32 bytes)
//   - Bytes 80-111: MChecksum (32 bytes)
func (m *KVStashMetadata) Serialize() []byte {
	var out = make([]byte, constants.MetadataSize)

	binary.BigEndian.PutUint64(out[0:8], uint64(m.Offset))
	binary.BigEndian.PutUint64(out[8:16], uint64(m.Size))
	binary.BigEndian.PutUint64(out[16:24], uint64(m.Flags))

	copy(out[24:56], m.SegmentFile[:])
	copy(out[56:88], m.Checksum[:])
	copy(out[88:120], m.MChecksum[:])

	return out[:]
}

// Deserialize populates the metadata fields from a byte array
// Expects exactly 112 bytes in the format produced by Serialize()
// Returns an error if the input data is not the correct size
func (m *KVStashMetadata) Deserialize(data []byte) error {
	if len(data) != constants.MetadataSize {
		return fmt.Errorf("Deserialize: data does not conform size")
	}

	m.Offset = int64(binary.BigEndian.Uint64(data[0:8]))
	m.Size = int64(binary.BigEndian.Uint64(data[8:16]))
	m.Flags = int64(binary.BigEndian.Uint64(data[16:24]))

	copy(m.SegmentFile[:], data[24:56])
	copy(m.Checksum[:], data[56:88])
	copy(m.MChecksum[:], data[88:120])

	return nil
}

// ValidateMChecksum verifies the integrity of the metadata by recomputing its checksum
// Returns an error if the computed checksum does not match the stored MChecksum
func (m *KVStashMetadata) ValidateMChecksum() error {
	var buf bytes.Buffer

	if err := binary.Write(&buf, binary.BigEndian, m.Offset); err != nil {
		return fmt.Errorf("ValidateMChecksum: failed to write offset: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, m.Size); err != nil {
		return fmt.Errorf("ValidateMChecksum: failed to write size: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, m.Flags); err != nil {
		return fmt.Errorf("ComputeChecksum: failed to write flags: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, m.SegmentFile); err != nil {
		return fmt.Errorf("ValidateMChecksum: failed to write segmentFile: %w", err)
	}
	if err := binary.Write(&buf, binary.BigEndian, m.Checksum); err != nil {
		return fmt.Errorf("ValidateMChecksum: failed to write checksum: %w", err)
	}

	if sha256.Sum256(buf.Bytes()) != m.MChecksum {
		return fmt.Errorf("ValidateMChecksum: metadata corrupted")
	}

	return nil
}

// fitFileName converts a filename string to a fixed 32-byte array
// Returns an error if the filename exceeds 32 bytes
// Shorter names are zero-padded on the right
func fitFileName(name string) ([32]byte, error) {
	var out [32]byte

	if len(name) > 32 {
		return out, fmt.Errorf("fitFileName: name too large")
	}

	copy(out[:], name)
	return out, nil
}

func (m *KVStashMetadata) GetMetadataFlagValue(flag int64) bool {
	return ((1 << flag) & m.Flags) > 0
}

func ComputeMetadataFlag(flags []int64) int64 {
	value := int64(0)

	if flags == nil {
		return value
	}

	for i := range flags {
		value = (value | 1<<flags[i])
	}

	return value
}

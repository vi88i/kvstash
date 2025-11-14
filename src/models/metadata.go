package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"kvstash/src/constants"
)

type KVStashMetadata struct {
	Offset      int64
	Size        int64
	SegmentFile [32]byte
	Checksum    [32]byte
	MChecksum   [32]byte
}

// BigEndian (network standard)
func (m *KVStashMetadata) ComputeChecksum(offset int64, size int64, fileName string, data []byte) error {
	fileNameBytes, err := fitFileName(fileName)
	if err != nil {
		return fmt.Errorf("ComputeChecksum: %w", err)
	}

	var buf1, buf2 bytes.Buffer

	binary.Write(&buf1, binary.BigEndian, offset)
	binary.Write(&buf1, binary.BigEndian, size)
	binary.Write(&buf1, binary.BigEndian, fileNameBytes)
	binary.Write(&buf1, binary.BigEndian, data)
	valueChecksum := sha256.Sum256(buf1.Bytes())

	binary.Write(&buf2, binary.BigEndian, offset)
	binary.Write(&buf2, binary.BigEndian, size)
	binary.Write(&buf2, binary.BigEndian, fileNameBytes)
	binary.Write(&buf2, binary.BigEndian, valueChecksum)
	metadataChecksum := sha256.Sum256(buf2.Bytes())

	m.Offset = offset
	m.Size = size
	m.SegmentFile = fileNameBytes
	m.Checksum = valueChecksum
	m.MChecksum = metadataChecksum
	return nil
}

func (m *KVStashMetadata) Serialize() []byte {
	var out = make([]byte, constants.MetadataSize)

	binary.BigEndian.PutUint64(out[0:8], uint64(m.Offset))
	binary.BigEndian.PutUint64(out[8:16], uint64(m.Size))

	copy(out[16:48], m.SegmentFile[:])
	copy(out[48:80], m.Checksum[:])
	copy(out[80:112], m.MChecksum[:])

	return out[:]
}

func (m *KVStashMetadata) Deserialize(data []byte) error {
	if len(data) != constants.MetadataSize {
		return fmt.Errorf("Deserialize: data does not conform size")
	}

	m.Offset = int64(binary.BigEndian.Uint64(data[0:8]))
	m.Size = int64(binary.BigEndian.Uint64(data[8:16]))

	copy(m.SegmentFile[:], data[16:48])
	copy(m.Checksum[:], data[48:80])
	copy(m.MChecksum[:], data[80:112])

	return nil
}

func (m *KVStashMetadata) ValidateMChecksum() error {
	var buf bytes.Buffer
	
	binary.Write(&buf, binary.BigEndian, m.Offset)
	binary.Write(&buf, binary.BigEndian, m.Size)
	binary.Write(&buf, binary.BigEndian, m.SegmentFile)
	binary.Write(&buf, binary.BigEndian, m.Checksum)

	if sha256.Sum256(buf.Bytes()) != m.MChecksum {
		return fmt.Errorf("ValidateMChecksum: metadata corrupted")
	}

	return nil
}

func fitFileName(name string) ([32]byte, error) {
	var out [32]byte

	if len(name) > 32 {
		return out, fmt.Errorf("fitFileName: name too large")
	}

	copy(out[:], name)
	return out, nil
}

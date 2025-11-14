package store

import (
	"encoding/json"
	"fmt"
	"io"
	"kvstash/src/constants"
	"kvstash/src/models"
	"log"
	"os"
	"path/filepath"
)

var index models.KVStashIndex
var writer *LogWriter

func init() {
	index = make(models.KVStashIndex)
	buildIndex()
	writer = NewLogWriter("db")
}

func Set(req *models.KVStashRequest) error {
	if len(req.Key) == 0 {
		return fmt.Errorf("key should not be empty")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("Set: failed to serialize")
	}
	offset, size, err := writer.Write(data)
	if err != nil {
		return fmt.Errorf("Set: %w", err)
	}

	index[req.Key] = &models.KVStashIndexEntry{
		SegmentFile: "active.log",
		Offset:      int64(offset),
		Size:        int64(size),
	}

	return nil
}

func Get(req *models.KVStashRequest) (string, error) {
	entry, ok := index[req.Key]
	if !ok {
		return "", fmt.Errorf("key not found in index")
	}

	value, err := fetchValue("db", entry.SegmentFile, entry.Offset, entry.Size)
	if err != nil {
		return "", fmt.Errorf("Get: %w", err)
	}

	return value, nil
}

func buildIndex() {
	file, err := os.OpenFile(filepath.Join("db", "active.log"), os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	buf := make([]byte, constants.MetadataSize)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			var metadata models.KVStashMetadata
			metadata.Deserialize(buf)

			if metadata.ValidateMChecksum() != nil {
				log.Println("metadata checksum failed")
				break
			}

			dataBytes := make([]byte, metadata.Size)
			file.Read(dataBytes)

			var data models.KVStashRequest
			if err := json.Unmarshal(dataBytes, &data); err != nil {
				log.Printf("buildIndex: deserialize failed - %v", err)
				break
			}

			log.Printf("buildIndex: read %v", data.Key)
			index[data.Key] = &models.KVStashIndexEntry{
				SegmentFile: "active.log",
				Offset:      metadata.Offset,
				Size:        metadata.Size,
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			panic(err)
		}
	}
}

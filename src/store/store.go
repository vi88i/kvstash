package store

import (
	"fmt"
	"kvstash/src/models"
)

var index models.KVStashIndex
var writer *LogWriter

func init() {
	index = make(models.KVStashIndex)
	writer = NewLogWriter("db")
}

func Set(req *models.KVStashRequest) error {
	if len(req.Key) == 0 {
		return fmt.Errorf("key should not be empty")
	}

	offset, size, err := writer.Write([]byte(req.Value))
	if err != nil {
		return fmt.Errorf("failed to write")
	}

	index[req.Key] = &models.KVStashIndexEntry{
		SegmentFile: "active.log",
		Offset: offset,
		Size: size,
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

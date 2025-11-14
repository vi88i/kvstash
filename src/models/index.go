package models

type KVStashIndexEntry struct {
	SegmentFile string
	Offset      int64
	Size        int64
}

type KVStashIndex = map[string]*KVStashIndexEntry

package models

type KVStashIndexEntry struct {
	SegmentFile string
	Offset      int64
	Size        int
}

type KVStashIndex = map[string]*KVStashIndexEntry

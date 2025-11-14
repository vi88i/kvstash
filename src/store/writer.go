package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

/*
Requirements:
- Multiple writers
- Multiple readers
- Durability vs Throughput

1. Throughput inversely proportional to durability
    A         B
f.Write()  f.Write()
f.Write()  f.Sync()
f.Write()  f.Write()
f.Sync()   f.Sync()
           f.Write()

Sync() - this step is used to flush the file metadata and data written to the file to the disk
we need not call it explicitly, the kernel internally does it for us (batching)
Reason: Writing to disk is expensive, so it is often done in batches to improve performance
If we want high durability (B), we need to compromise on throughput (call sync after every write, synchronous I/O)
If we want high throughput (A), we need to compromise on durability (are we okay with losing some writes on power outage?)

When file is opened with O_SYNC it enables synchronous I/O

2. Multiple writers
???

3. Multiple Readers
???
*/

type LogWriter struct {
	file   *os.File
	offset int64
	mu     sync.Mutex
}

func NewLogWriter(dbPath string) *LogWriter {
	logPath := filepath.Join(dbPath, "active.log")

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_SYNC|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		panic(err)
	}

	return &LogWriter{file: file, offset: info.Size()}
}

func (lw *LogWriter) Write(data json.RawMessage) (int64, int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	log.Printf("Write: Writing at %v", lw.offset)
	offset := lw.offset
	n, err := lw.file.WriteAt(data, offset)
	if err != nil {
		return 0, 0, fmt.Errorf("Write: %w", err)
	}
	lw.offset += int64(n)

	return offset, n, nil
}

func (lw *LogWriter) Close() error {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if err := lw.file.Close(); err != nil {
		return err
	}

	return nil
}

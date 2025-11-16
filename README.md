# KVStash

A high-performance, persistent key-value store inspired by Bitcask. KVStash provides a simple HTTP API for storing and retrieving data with strong durability guarantees and automatic data corruption detection.

## Features

- **Append-only log design** - Simple, fast writes with strong durability
- **In-memory index** - O(1) lookups without scanning disk
- **Automatic log rotation** - Prevents unbounded file growth
- **Dual checksum validation** - SHA-256 checksums for both metadata and data
- **Thread-safe operations** - Concurrent reads and writes supported
- **Corruption detection** - Automatic detection and handling of corrupted data
- **Graceful degradation** - Tolerates corruption in active log during crash recovery

## Quick Start

### Build and Run

```bash
cd src
go build -o kvstash
./kvstash
```

The server will start on `http://localhost:8080`

### Configuration

Edit `src/constants/metadata.go` and `src/constants/segment.go`:

```go
// Database configuration
DBPath = "../db"           // Database directory
MaxKeySize = 256           // Maximum key size (bytes)
MaxValueSize = 1048576     // Maximum value size (1 MB)
MaxKeysPerSegment = 3      // Writes per segment before rotation
```

## API Reference

### Set a Key-Value Pair

**Endpoint:** `POST /kvstash`

**Request:**
```json
{
  "key": "username",
  "value": "john_doe"
}
```

**Response (201 Created):**
```json
{
  "success": true,
  "message": "",
  "data": null
}
```

**Error Responses:**
- `400 Bad Request` - Empty key, key/value too large, or invalid JSON
- `500 Internal Server Error` - Write failure

### Get a Value

**Endpoint:** `GET /kvstash`

**Request:**
```json
{
  "key": "username"
}
```

**Response (200 OK):**
```json
{
  "success": true,
  "message": "",
  "data": {
    "key": "username",
    "value": "john_doe"
  }
}
```

**Error Responses:**
- `404 Not Found` - Key doesn't exist
- `500 Internal Server Error` - Read failure or data corruption

### Example Usage

```bash
# Set a value
curl -X POST http://localhost:8080/kvstash \
  -H "Content-Type: application/json" \
  -d '{"key":"user:1","value":"Alice"}'

# Get a value
curl -X GET http://localhost:8080/kvstash \
  -H "Content-Type: application/json" \
  -d '{"key":"user:1"}'

# Update a value (same as set)
curl -X POST http://localhost:8080/kvstash \
  -H "Content-Type: application/json" \
  -d '{"key":"user:1","value":"Bob"}'
```

## Architecture

### Storage Format

KVStash uses an append-only log with the following structure:

```
[Metadata 112 bytes][Value N bytes][Metadata 112 bytes][Value M bytes]...
```

**Metadata Structure (112 bytes):**
- Offset (8 bytes) - Byte position of value data
- Size (8 bytes) - Length of value data
- SegmentFile (32 bytes) - Name of containing file
- Checksum (32 bytes) - SHA-256 of value data
- MChecksum (32 bytes) - SHA-256 of metadata

### Log Rotation

When the active log reaches `MaxKeysPerSegment` writes:
1. Current active log is closed (becomes an archived segment)
2. New segment file is created (e.g., seg0.log → seg1.log → seg2.log)
3. activeLogCount resets to 0
4. Writes continue to the new active log

**Segment naming:** `seg0.log`, `seg1.log`, `seg2.log`, etc. (0-indexed)

### Index Structure

In-memory hash map for O(1) lookups:

```go
map[string]*IndexEntry {
    "key" -> {
        SegmentFile: "seg2.log",
        Offset: 1000,
        Size: 256,
        Checksum: [32]byte{...}
    }
}
```

### Data Integrity

**Dual Checksum System:**
1. **Value checksum** = SHA-256(offset || size || fileName || data)
2. **Metadata checksum** = SHA-256(offset || size || fileName || valueChecksum)

**On Write:**
- Both checksums computed and stored with metadata
- Offset rollback on partial write failures

**On Read:**
- Metadata checksum validated during index building
- Value checksum validated on every read operation
- Corrupted entries automatically purged from index

### Crash Recovery

**Active Log Corruption:**
- Tolerates corruption in the active log (expected during crashes)
- Reads all valid entries before corruption point
- Logs error but continues startup
- Allows graceful degradation

**Archived Segment Corruption:**
- Fails fast on corruption in archived segments (unexpected)
- Clears entire index and returns error
- Prevents serving potentially incorrect data
- Requires manual intervention

## Design Decisions

### Why Append-Only?

- **Simplicity** - No in-place updates, no fragmentation
- **Crash safety** - Partial writes don't corrupt existing data
- **Fast writes** - Sequential I/O is faster than random
- **Easy recovery** - Replay log to rebuild index

### Why In-Memory Index?

- **Fast lookups** - O(1) without disk seeks
- **Small footprint** - Only metadata, not actual values
- **Quick startup** - Index rebuilt by scanning logs once

### Why Log Rotation?

- **Bounded file size** - Prevents single file from growing too large
- **Easier compaction** - Can merge/compact old segments separately
- **Better performance** - Smaller files for OS to manage

### Why Dual Checksums?

- **Metadata protection** - Detect corruption during index building
- **Value protection** - Detect corruption during reads
- **Fail fast** - Identify corruption early rather than serving bad data

### Limitations

- **No deletion** - Keys cannot be deleted (only updated)
- **No compaction** - Old values accumulate (future feature)
- **Memory overhead** - Entire index must fit in RAM
- **Single server** - No replication or clustering (yet)

## Future Enhancements

- [ ] Compaction/garbage collection
- [ ] Delete operation support
- [ ] Range queries
- [ ] Snapshots
- [ ] Replication
- [ ] Compression

## License

This is a personal learning project.

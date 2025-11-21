# KVStash

A high-performance, persistent key-value store inspired by Bitcask. KVStash provides a simple HTTP API for storing and retrieving data with strong durability guarantees, automatic compaction, and data corruption detection.

## Table of Contents

- [Features](#features)
- [Quick Start](#quick-start)
- [API Reference](#api-reference)
- [Architecture](#architecture)
  - [Storage Format](#storage-format)
  - [Log Rotation](#log-rotation)
  - [Index Structure](#index-structure)
  - [Data Integrity](#data-integrity)
  - [Crash Recovery](#crash-recovery)
  - [Automatic Compaction](#automatic-compaction)
- [Design Decisions](#design-decisions)
- [Load Testing](#load-testing)
- [Future Enhancements](#future-enhancements)
- [License](#license)

## Features

- **Append-only log design** - Simple, fast writes with strong durability
- **In-memory index** - O(1) lookups without scanning disk
- **Tombstone-based deletion** - Delete keys with persistent tombstone records
- **Automatic log rotation** - Prevents unbounded file growth
- **Automatic compaction** - Periodic garbage collection reclaims disk space
- **Dual checksum validation** - SHA-256 checksums for both metadata and data
- **Thread-safe operations** - Concurrent reads, writes, and deletes supported
- **Corruption detection** - Automatic detection and handling of corrupted data
- **Graceful degradation** - Tolerates corruption in active log during crash recovery
- **Crash recovery** - Automatic backup and recovery mechanisms

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
DBPath = "../db"              // Database directory
MaxKeySize = 256              // Maximum key size (bytes)
MaxValueSize = 1048576        // Maximum value size (1 MB)
MaxKeysPerSegment = 3         // Writes per segment before rotation
CompactionInterval = 60       // Compaction interval (seconds)
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

### Delete a Key

**Endpoint:** `DELETE /kvstash`

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
  "data": null
}
```

**Error Responses:**
- `400 Bad Request` - Empty key or key too large
- `404 Not Found` - Key doesn't exist
- `500 Internal Server Error` - Delete failure

**How Deletion Works (Soft Delete):**
- Writes a tombstone record to the append-only log with the `FlagDeleted` marker
- Marks the key in the in-memory index with `Deleted=true` (soft delete)
- Key remains in index pointing to tombstone location (ensures compaction works even when all keys deleted)
- GET operations return 404 Not Found for soft-deleted keys
- Tombstone is replayed during recovery to restore the `Deleted=true` state
- During compaction, soft-deleted entries are skipped and not copied to the new store
- Physical disk space is reclaimed when old segments are removed during compaction

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

# Delete a key
curl -X DELETE http://localhost:8080/kvstash \
  -H "Content-Type: application/json" \
  -d '{"key":"user:1"}'
```

## Architecture

### Storage Format

KVStash uses an append-only log with the following structure:

```
[Metadata 112 bytes][Value N bytes][Metadata 112 bytes][Value M bytes]...
```

**Metadata Structure (120 bytes):**
- Offset (8 bytes) - Byte position of value data
- Size (8 bytes) - Length of value data
- Flags (8 bytes) - Operation flags (bit 0 = deleted/tombstone)
- SegmentFile (32 bytes) - Name of containing file
- Checksum (32 bytes) - SHA-256 of value data
- MChecksum (32 bytes) - SHA-256 of metadata

### Tombstone Deletion (Soft Delete)

KVStash uses a **soft-delete** approach where deleted keys remain in the index but are marked as deleted.

**Tombstone Structure:**
- Metadata with `FlagDeleted` (bit 0 set)
- Value contains only the key: `{"key":"username","value":""}`
- Size reflects the JSON-encoded key size (~15-20 bytes)

**Soft Delete Flow:**
1. DELETE request received for key "foo"
2. Tombstone written to active log with FlagDeleted=true
3. Index entry updated with `Deleted=true` (soft delete, entry stays in index)
4. GET operations check the `Deleted` flag and return 404 if true
5. On server restart: log replay processes tombstone and creates entry with `Deleted=true`
6. During compaction: entries with `Deleted=true` are skipped (not copied)
7. After compaction: old segments with tombstones are removed (space reclaimed)

**Why Soft Delete:**
- Ensures compaction works correctly even when all keys are deleted
- Allows tracking of disk space used by tombstones
- Maintains consistency between runtime and post-restart states
- Simplifies corruption handling (mark corrupted entries as deleted)

**Example Log After Delete:**
```
[Metadata][{"key":"foo","value":"bar"}]     ← Original SET
[Metadata][{"key":"foo","value":"baz"}]     ← UPDATE
[Metadata+FlagDeleted][{"key":"foo","value":""}]  ← DELETE (tombstone)
```

### Log Rotation

When the active log reaches `MaxKeysPerSegment` writes:
1. Current active log is closed (becomes an archived segment)
2. New segment file is created (e.g., seg0.log → seg1.log → seg2.log)
3. activeLogCount resets to 0
4. Writes continue to the new active log

**Segment naming:** `seg0.log`, `seg1.log`, `seg2.log`, etc. (0-indexed)

### Index Structure

In-memory hash map for O(1) lookups with soft-delete support:

```go
map[string]*IndexEntry {
    "active_key" -> {
        SegmentFile: "seg2.log",
        Offset: 1000,
        Size: 256,
        Checksum: [32]byte{...},
        Deleted: false      // Live key
    },
    "deleted_key" -> {
        SegmentFile: "seg3.log",
        Offset: 2000,
        Size: 18,
        Checksum: [32]byte{...},
        Deleted: true       // Soft-deleted (tombstone)
    }
}
```

**Key Points:**
- Deleted entries remain in the index with `Deleted=true`
- GET operations check the `Deleted` flag and return 404 if true
- Compaction skips entries with `Deleted=true` to reclaim space
- This ensures compaction works even when all keys are deleted

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

### Automatic Compaction

KVStash implements periodic compaction to reclaim disk space from old/updated values.

**Compaction Process:**

1. **Backup Creation** - Database copied to `BackupDBPath` before any modifications
2. **New Store Creation** - Temporary store created at `TmpDBPath`
3. **Data Copy** - Only current (non-deleted, non-stale) key-value pairs copied to new store
4. **Atomic Swap** - Old database replaced with compacted version
5. **Cleanup** - Backup removed on success, restored on failure

**What Gets Removed:**
- Old values for updated keys
- Tombstones for deleted keys
- Deleted key data (keys not in current index)

**Lock Strategy:**
- Global store lock held during entire compaction cycle
- Ensures data consistency during database swap
- **Trade-off:** All Get/Set operations blocked during compaction
- Compaction duration depends on number of active keys

**Timing:**
- Runs automatically every `CompactionInterval` seconds (default: 60s)
- Only spawned for main database (not temporary stores)
- Single compaction goroutine per store instance

**Disk Space Savings:**
- Eliminates old values for updated keys
- Removes tombstones and deleted key data
- Removes stale entries from rotated segments
- Defragments data across fewer segment files
- Example: 1000 writes to same key → compacted to 1 entry
- Example: Deleted key with tombstone → completely removed

**Recovery Mechanisms:**

**Disaster Recovery (On Startup):**
```
Database missing but backup exists → Restore from backup → Delete backup
```
- Handles crash during compaction after backup created
- Automatic recovery, no manual intervention needed
- Panics if recovery fails (database unrecoverable)

**Compaction Failure Recovery:**
```
Compaction fails → Close new store → Restore from backup → Recreate writer
```
- Backup restored if compaction swap fails
- Database returned to pre-compaction state
- Panics if backup restoration fails

**Error Handling:**
- **Backup creation failure:** Skip cycle, retry next interval
- **New store creation failure:** Skip cycle, retry next interval
- **Data copy failure:** Clean up resources, skip cycle
- **Database swap failure:** Restore from backup
- **Recovery failure:** Panic (unrecoverable state)

**Performance Impact:**
- **During compaction:** Latency spike visible in P95/P99 metrics
- **After compaction:** Improved read/write performance due to fewer segment files
- **Throughput:** Temporary drop during lock acquisition
- **Disk I/O:** Spike during backup creation and data copy

**Monitoring Compaction:**

Watch server logs for compaction messages:
```
autoCompact: done                                      # Successful compaction completed
autoCompact: backup failed: <error>                    # Backup failed, skipping
autoCompact: creating new store failed: <error>        # Store creation failed
autoCompact: failed to fetch <key>: <error>            # Data copy failed
autoCompact: failed to rename tmp db: <error>          # Swap failed, recovering
autoCompact: skipping store replacement                # Cleanup after failure
```

**Success Indicator:**
- Successful compaction logs `autoCompact: done` after completion
- Deleted entries are removed from disk
- Index is updated with the compacted store's metadata

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

### Why Soft-Delete with Tombstones?

- **Append-only compatibility** - Deletions fit naturally into the log structure
- **Crash safety** - Tombstones are replayed on recovery to restore delete operations
- **Compaction reliability** - Soft-deleted entries ensure compaction works even when all keys deleted
- **Space tracking** - Index tracks disk space used by tombstones for monitoring
- **Corruption handling** - Corrupted entries can be marked as deleted and cleaned up during compaction
- **Consistency** - Same behavior before and after restart (deleted keys always have `Deleted=true`)
- **Eventual cleanup** - Deleted entries physically removed during next compaction cycle

### Limitations

- **Compaction blocks operations** - Global lock during compaction blocks all reads/writes/deletes
- **Memory overhead** - Entire index must fit in RAM (including soft-deleted entries until compaction)
- **Single server** - No replication or clustering (yet)
- **Windows file handles** - Requires delays for directory operations on Windows
- **Tombstone overhead** - Deleted keys occupy both disk space and memory until next compaction cycle

### Compaction Trade-offs

**Advantages:**
- ✅ Automatic disk space reclamation
- ✅ Backup/recovery mechanisms built-in
- ✅ No external compaction tools needed
- ✅ Guaranteed data consistency during swap

**Disadvantages:**
- ❌ All operations blocked during compaction (global lock)
- ❌ Periodic latency spikes every `CompactionInterval` seconds
- ❌ Requires 2x disk space (original + backup)
- ❌ Compaction time increases with number of keys

**When Compaction Hurts Performance:**
- High-frequency read/write workloads
- Latency-sensitive applications (SLA < 100ms)
- Large datasets (millions of keys)
- Low `CompactionInterval` values

**Optimization Strategies:**
- Increase `CompactionInterval` to reduce frequency
- Increase `MaxKeysPerSegment` to reduce segment count
- Run compaction during low-traffic windows
- Consider lock-free compaction (future enhancement)

## Future Enhancements

- [ ] Lock-free compaction (background incremental compaction)
- [ ] Range queries
- [ ] Point-in-time snapshots
- [ ] Replication and clustering
- [ ] Compression
- [ ] Bloom filters for faster negative lookups
- [ ] Metrics endpoint (Prometheus format)
- [ ] Optimized tombstone handling (batch deletion during compaction)

## License

This is a personal learning project.

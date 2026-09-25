# Database Executor

A simple DBMS built from scratch in Go. Not meant for production — only for learning how databases work internally.

## Main Resource

- **PostgreSQL Internals** — https://www.interdb.jp/pg/

## What You'll Learn

### Query Execution (Volcano Model)
How databases process queries one row at a time. Each operator (scan, filter, sort) implements the same interface and pulls data from its child when asked.

### Storage Engine
How data lives on disk:
- **Slotted pages** — Fixed 4096-byte pages with line pointers, null bitmaps, and records growing inward from both ends
- **Record encoding** — Binary format with pascal strings (length-prefixed)
- **Heap files** — Pages stacked sequentially, scanned front to back

### Indexing (B+ Tree)
How databases speed up lookups:
- **Insert/Search** — Navigate internal nodes to find the right leaf
- **TID Pointers** — Index stores Tuple IDs (page + slot), not full tuples
- **Range scan** — Leaf nodes are linked, so scanning 100-200 is just walking the list
- **Delete** — Borrow/merge nodes to keep the tree balanced
- **DeleteByTID** — Remove index entries pointing to a specific tuple (used by VACUUM)
- **Persistence** — Save/load tree to/from disk

### MVCC (Multi-Version Concurrency Control)
How databases handle concurrent reads and writes:
- **Versioned records** — Each record has TxMin (creator), TxMax (deleter), CID (command id)
- **Visibility rules** — Determine which version a transaction can see
- **Commit log (clog)** — Track which transactions committed/aborted
- **Snapshot isolation** — Each transaction sees a consistent snapshot of data
- **xip_list** — Snapshot of in-progress transactions at snapshot time (avoids seeing uncommitted changes)

### Isolation Levels
- **Repeatable Read** — xip_list fixed at transaction start, all statements see same data
- **Read Committed** — xip_list refreshed at each statement via `NewStatement()`, may see different data

### VACUUM (Garbage Collection)
How databases clean up dead tuples:
- **Dead tuple detection** — Scan heap to find tuples with committed deletions
- **Freeze processing** — Prevent transaction ID wraparound by freezing old tuples
- **Index cleanup** — Remove orphaned index entries pointing to dead tuples
- **Heap cleanup** — Mark dead slots and update Free Space Map (FSM)

### Query Operators
Each operator is a small, composable unit:

| Operator | What It Does |
|----------|--------------|
| `MemoryScan` | Iterates over an in-memory slice |
| `HeapFileScan` | Reads binary slotted pages, supports MVCC visibility filtering |
| `Selection` | Filters rows with a predicate |
| `Projection` | Picks/transforms columns |
| `Sort` | Buffers all rows, sorts, emits one at a time |
| `Limit` | Stops after N rows |
| `Insert` | Adds a record to a file, supports MVCC with automatic clog logging |
| `BTreeScan` | Walks a B+ tree's linked leaves, uses TupleReader with MVCC visibility |

## Project Structure

```
database/
├── executors/            # Query operators
├── storage/              # Pages, records, file I/O
├── tx/                   # Transaction management, commit log
├── btree/                # B+ tree index
├── vacuum/               # VACUUM (garbage collection)
├── learn/                # Entry point — run and test things here
├── movies.csv            # Sample data (27K movies)
└── go.mod
```

## How to Run

```bash
# Unit tests (storage, executors, btree, tx, vacuum)
go test ./...

# Learn — run and step through code
go test ./learn/... -v
```

## Example Query

```go
// Scan → filter Comedy → pick title → limit 5
scan, _ := executors.NewHeapFileScan("movies.data")
filtered := executors.NewSelection(scan, func(t storage.Tuple) bool {
    return strings.Contains(t[2].(string), "Comedy")
})
projected := executors.NewProjection(filtered, func(t storage.Tuple) storage.Tuple {
    return storage.Tuple{t[1]}
})
limited := executors.NewLimit(projected, 5)

results, _ := executor.Run(limited)
// → ["Toy Story (1995)", "Jumanji (1995)", ...]
```

## MVCC Example

```go
// Create commit log and TxManager
clog, _ := tx.OpenCommitLog("clog.data")
defer clog.Close()
mgr := tx.NewTxManager()

// tx=1: INSERT with MVCC context
tx1 := mgr.Begin(tx.RepeatableRead)
ctx1 := &executors.TransactionContext{
    Tx:       tx1,
    Clog:     clog,
    Snapshot: tx1.Id(),
    XipList:  tx1.XipList(),
}

// Insert record (automatically creates TxRecord and logs to clog)
insert := executors.NewInsert("movies.data", storage.MovieRecord{
    MovieId: 1,
    Title:   "Toy Story",
    Genres:  "Animation",
}, 0, ctx1)
insert.Next()
mgr.Commit(tx1)

// tx=2: Scan with MVCC context (sees committed records)
tx2 := mgr.Begin(tx.RepeatableRead)
ctx2 := &executors.TransactionContext{
    Tx:       tx2,
    Clog:     clog,
    Snapshot: tx2.Id(),
    XipList:  tx2.XipList(),
}

scan, _ := executors.NewHeapFileScan("movies.data", ctx2)
results, _ := executor.Run(scan)
// → [{1, "Toy Story", "Animation"}]
```

## VACUUM Example

```go
// Create commit log
clog, _ := tx.OpenCommitLog("clog.data")
defer clog.Close()

// Create B+ tree index (optional)
bt := btree.NewBTree()
bt.Insert(100, storage.TID{PageId: 0, SlotId: 0})
bt.Insert(200, storage.TID{PageId: 0, SlotId: 1})

// Run complete VACUUM
// Parameters: heap file, commit log, index, FSM path, xip list, current txID, freeze age
stats, err := vacuum.Vacuum("movies.data", clog, bt, "movies.fsm",
    map[uint64]bool{}, 100, 50)

fmt.Printf("Dead tuples: %d\n", stats.DeadTuples)
fmt.Printf("Frozen tuples: %d\n", stats.FrozenTuples)
fmt.Printf("Index entries removed: %d\n", stats.IndexEntries)
```

## On-Disk Format

### Page Layout (4096 bytes)

```
┌─────────────────────────────┐
│ Page Header (8 bytes)       │
│   pageId      [0-3]        │
│   recordCount [4-5]        │
│   freeOffset  [6-7]        │
├─────────────────────────────┤
│ Line Pointers (4 bytes each)│ → grow forward
│   offset [0-1]              │
│   length [2-3]              │
├─────────────────────────────┤
│ Null Bitmaps (1 byte each)  │
│   bit 0 = movieId NULL      │
│   bit 1 = title NULL        │
│   bit 2 = genres NULL       │
├─────────────────────────────┤
│                             │
│   Free Space                │
│                             │
├─────────────────────────────┤
│ Records                     │ ← grow backward
└─────────────────────────────┘
```

### File Layout

```
[4 bytes] record count
[4096 bytes] page 0
[4096 bytes] page 1
...
```

### FSM File Layout (Free Space Map)

```
[4 bytes] page count (uint32)
[2 bytes] page 0 free space (uint16)
[2 bytes] page 1 free space (uint16)
...
```

**Purpose:** Track free space per page for O(1) INSERT lookup.

**Without FSM:** INSERT scans all pages O(n)
**With FSM:** INSERT does O(1) lookup

### TID Layout (Tuple ID)

```
┌─────────────────────────────────┐
│ TID (8 bytes)                   │
├─────────────────────────────────┤
│ PageId  [0-3]  (4 bytes)       │
│ SlotId  [4-5]  (2 bytes)       │
│ (padding) [6-7] (2 bytes)      │
└─────────────────────────────────┘
```

**Purpose:** Uniquely identify a tuple in a heap file. Used by indexes to point to tuples.

## TxRecord Layout

```
┌─────────────────────────────────────────┐
│ TxRecord (versioned record)             │
├─────────────────────────────────────────┤
│ TxMin    [0-7]   (8 bytes)  creator tx  │
│ TxMax    [8-15]  (8 bytes)  deleter tx  │
│ CID      [16-19] (4 bytes)  command id  │
│ DataLen  [20-23] (4 bytes)  data length │
│ Data     [24-..] (N bytes)  tuple data  │
└─────────────────────────────────────────┘
```

## TODO

- [x] Refactor all tests + `cmd/main.go` into proper `_test.go` files
- [ ] Query Planner (rule-based scan selection, cost estimation)
- [ ] JOIN operators (Nested Loop, Hash Join, Sort-Merge Join)
- [x] Integrate transactions into executors (automatic clog logging)
- [ ] Buffer Pool (LRU cache, page eviction, flush clog/memory to disk)
- [ ] Write-Ahead Logging (WAL)
- [ ] Locking (row-level locks, gap locks)
- [x] VACUUM (dead tuple detection, freeze processing, index/heap vacuuming)
- [x] xip_list (snapshot of in-progress transactions)

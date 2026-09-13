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
- **Range scan** — Leaf nodes are linked, so scanning 100-200 is just walking the list
- **Delete** — Borrow/merge nodes to keep the tree balanced
- **Persistence** — Save/load tree to/from disk

### MVCC (Multi-Version Concurrency Control)
How databases handle concurrent reads and writes:
- **Versioned records** — Each record has TxMin (creator), TxMax (deleter), CID (command id)
- **Visibility rules** — Determine which version a transaction can see
- **Commit log (clog)** — Track which transactions committed/aborted
- **Snapshot isolation** — Each transaction sees a consistent snapshot of data

### Isolation Levels
- **Repeatable Read** — Snapshot taken at transaction start, all statements see same data
- **Read Committed** — Snapshot taken at each statement, may see different data

### Query Operators
Each operator is a small, composable unit:

| Operator | What It Does |
|----------|--------------|
| `MemoryScan` | Iterates over an in-memory slice |
| `HeapFileScan` | Reads binary slotted pages |
| `Selection` | Filters rows with a predicate |
| `Projection` | Picks/transforms columns |
| `Sort` | Buffers all rows, sorts, emits one at a time |
| `Limit` | Stops after N rows |
| `Insert` | Adds a record to a file |
| `BTreeScan` | Walks a B+ tree's linked leaves |

## Project Structure

```
database/
├── executors/            # Query operators
├── storage/              # Pages, records, file I/O
├── tx/                   # Transaction management, commit log
├── btree/                # B+ tree index
├── learn/                # Integration tests
├── movies.csv            # Sample data (27K movies)
└── go.mod
```

## How to Run

```bash
# Unit tests (storage, executors, btree, tx)
go test ./...

# Integration tests (learn/)
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
// Create commit log
clog, _ := tx.OpenCommitLog("clog.data")
defer clog.Close()

// tx=1: INSERT A, COMMIT
recA := storage.InsertTxRecord(1, 0, storage.Tuple{"A"})
clog.LogCommit(1)

// tx=2: INSERT B (NOT committed)
recB := storage.InsertTxRecord(2, 0, storage.Tuple{"B"})
// clog.LogCommit(2) — not committed!

// tx=1 sees [A] (committed before tx=1 started)
recA.Visible(1, clog) // true

// tx=2 sees [A, B] (A committed, B is own change)
recA.Visible(2, clog) // true
recB.Visible(2, clog) // true

// tx=3 sees [A] (B not committed)
recA.Visible(3, clog) // true
recB.Visible(3, clog) // false
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
- [ ] JOIN operators (Nested Loop, Hash Join, Sort-Merge Join)
- [ ] Integrate transactions into executors (automatic clog logging)
- [ ] Buffer Pool (LRU cache, page eviction, flush clog/memory to disk)
- [ ] Write-Ahead Logging (WAL)
- [ ] Locking (row-level locks, gap locks)
- [ ] VACUUM (clean up dead tuples)
- [ ] xip_list (snapshot of in-progress transactions)

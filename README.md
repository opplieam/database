# Database Executor

A simple DBMS built from scratch in Go. Not meant for production — only for learning how databases work internally.

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

### Query Operators
Each operator is a small, composable unit:

| Operator | What It Does |
|----------|--------------|
| `MemoryScan` | Iterates over an in-memory slice |
| `FileScan` | Reads a CSV file row by row |
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
├── cmd/main.go           # Integration test
├── executors/            # Query operators
├── storage/              # Pages, records, file I/O
├── btree/                # B+ tree index
├── movies.csv            # Sample data (27K movies)
└── go.mod
```

## How to Run

```bash
# Unit tests (storage, executors, btree)
go test ./...

# Integration test (loads CSV, builds pages, inserts, queries, indexes)
go run ./cmd/
```

## Example Query

```go
// Scan → filter Comedy → pick title → limit 5
scan, _ := executors.NewFileScan("movies.csv")
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

# Database: Learn DBMS Internals by Coding

A from-scratch database in Go, built concept by concept to understand how
databases work inside. Not production. Every topic pairs its interdb
chapter with runnable code: read the concept, run the test, break things,
inspect variables, repeat.

Main source: interdb (a free PostgreSQL internals book) at https://www.interdb.jp/pg/.

## How to learn with this repo

Prerequisites: you should already use PostgreSQL a little, tables and
inserts at least. This repo is about how the engine works inside, not
how to write SQL.

1. Read the linked interdb chapter section.
2. Run the linked `learn/` test with `-v` and read its step logs.
3. Re-run one test with `-run` and inspect the code it exercises.
4. Put a breakpoint in the test or the code under it and watch variables
   change round by round (e.g. Delve: `dlv test ./learn -- -test.run TestBufferSuite -v`).

```bash
go test ./...                                # everything
go test ./learn/ -v                          # step-by-step learning logs
go test ./learn/ -run TestBufferSuite -v     # one topic at a time
```

## Topics

- [Query operators](#query-operators) · [Storage](#storage) · [B+ tree index](#b-tree-index) · [MVCC and isolation](#mvcc-and-isolation) · [VACUUM](#vacuum) · [Buffer pool](#buffer-pool) · [WAL and crash recovery](#wal-and-crash-recovery)

Each topic is a simplified version of its interdb chapter for learning,
not a port. The pattern is the same everywhere: single-threaded, no
background processes, manual triggers where PostgreSQL uses daemons.

### Query operators

A query says which rows it wants. It never says how to fetch them. So
every operator answers the same question, "give me your next row," and
hands one row upward. Memory stays flat no matter the table size, because
rows flow instead of piling up. The exception proves the rule: sort buffers
everything, since the first row out cannot be known before the last row in.
New operators snap in without touching old ones because the question never
changes.

```go
scan, _ := executors.NewHeapFileScan("movies.data", nil)
filtered := executors.NewSelection(scan, func(t storage.Tuple) bool {
    return strings.Contains(t[2].(string), "Comedy")
})
limited := executors.NewLimit(filtered, 5)
results, _ := executors.Run(limited)
```

**Read:** [Ch 3](https://www.interdb.jp/pg/pgsql03.html)

**Run:** `go test ./learn/ -run TestExecutorSuite -v` ([test](learn/executor_integration_test.go))

**Tested:** scan, selection, projection, and limit compose by hand into one
chain (TestExecutorSuite). No operator holds resources between Next calls,
so Limit can stop early without cleanup.

**Limits:** no planner (composition is manual in tests) and no JOINs
(parked under Ch 3 in the TODO).

### Storage

A row needs an address that survives restarts. That address is page plus
slot, written down as a TID like {Page 2, Slot 5}. Pages are fixed
4096-byte boxes. Each box opens with a header (id, record count, free
offset), then line pointers mapping slots to byte offsets, then the
records packed from the rear. A sidecar file counts free bytes per page,
so an insert finds room with one lookup instead of reading the whole file.

Page layout (4096 bytes):

```
┌─────────────────────────────┐
│ Page Header (16 bytes)      │
│   pageId      [0-3]        │
│   recordCount [4-5]        │
│   freeOffset  [6-7]        │
│   pageLSN     [8-15]       │
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

File layout:

```
[4 bytes] page count (uint32)
[4096 bytes] page 0
[4096 bytes] page 1
...
```

FSM file layout (Free Space Map):

```
[4 bytes] page count (uint32)
[2 bytes] page 0 free space (uint16)
[2 bytes] page 1 free space (uint16)
...
```

Without FSM, INSERT scans all pages O(n). With FSM, one map lookup.

**Read:** [Ch 1](https://www.interdb.jp/pg/pgsql01.html)

**Run:** `go test ./learn/ -run TestHeapSuite -v` ([test](learn/heap_integration_test.go))

**Tested:** a single record layout for all rows, and FSM lookups that find
room with one map check (TestFSMIntegration).

**Limits:** no TOAST (PostgreSQL's overflow storage for large fields), no
compression, and no concurrent writers (single-threaded by design).

### B+ tree index

Reading every row works until the table grows. Then it falls over. The
tree keeps integer keys sorted across leaf pages chained together, so a
lookup or a range walks straight to its rows. Each key points at a heap
address (a TID). Nothing fancier, which keeps the code small enough to
read whole.

TID layout (Tuple ID, what index entries point to):

```
┌─────────────────────────────────┐
│ TID (8 bytes)                   │
├─────────────────────────────────┤
│ PageId  [0-3]  (4 bytes)       │
│ SlotId  [4-5]  (2 bytes)       │
│ (padding) [6-7] (2 bytes)      │
└─────────────────────────────────┘
```

**Read:** own design (no interdb chapter covers it)

**Run:** `go test ./learn/ -run TestBTreeSuite -v` ([test](learn/btree_integration_test.go))

**Tested:** the tree survives restarts balanced through borrow and merge on
delete plus save/load (TestBTreeSuite).

**Limits:** integer keys only, no concurrent access, and leaves carry
addresses rather than row data.

### MVCC and isolation

MVCC (Multi-Version Concurrency Control) lets readers not wait for
writers. Each row version stamps who created it and who deleted it. A log on disk records how transactions ended.
Every transaction takes a snapshot of the running set and judges each row
against it. Snapshot per statement gives read committed. Snapshot once
gives repeatable read. Old versions pile up because open snapshots still
see them. That pile is the price of never waiting, and VACUUM collects it.

TxRecord layout (versioned record):

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

**Read:** [Ch 5](https://www.interdb.jp/pg/pgsql05.html)

**Run:** `go test ./learn/ -run TestMvccSuite -v` ([integration](learn/mvcc_integration_test.go))

**Run:** `go test ./learn/ -run TestMvccInsertSuite -v` ([insert](learn/mvcc_insert_test.go))

**Tested:** snapshots list in-progress transactions (xip_list), and that
list is what separates read committed from repeatable read
(TestMvccSuite). Committed inserts stay visible across transactions
(TestMvccInsertSuite).

**Limits:** no serializable isolation, and no UPDATE or DELETE yet, so row
locks and lost-update handling wait for writers (see write-side
concurrency in the TODO).

### VACUUM

This database needs maintenance, because nothing overwrites here. Every
update inserts a brand-new version and stamps the old one deleted, so dead
rows accumulate and scans slow down stepping over them. VACUUM walks the
heap on demand and finds what no transaction can still see. It hands the
space back to the free-space map, freezes ancient transaction ids so the
counter never wraps, and removes index entries pointing at dead rows. No
daemon exists yet. Someone runs it by hand.

**Read:** [Ch 6](https://www.interdb.jp/pg/pgsql06.html)

**Run:** `go test ./learn/ -run TestVacuumSuite -v` ([test](learn/vacuum_integration_test.go))

**Tested:** dead detection (TestIsDead, TestScanHeap), freezing
(TestShouldFreeze, TestApplyFreezes), index and heap cleanup
(TestVacuumIndexes, TestVacuumHeap, TestFullVacuumFlow), and cost tracking
on real pool counters (TestCostReporterThrottles).

**Limits:** no VACUUM FULL, no visibility map, no autovacuum daemon. Costs
accrue through Sync, but VACUUM still reads around the pool instead of
through it.

### Buffer pool

Disk is slow and memory is not. Reading the same page twice from
disk wastes the gap between them. The pool holds a few pages in memory.
Second reads find them there. Writes scribble on the copy and mark it
dirty. Disk catches up on eviction or when someone calls flush. Every
borrow pairs Get with Unpin. Tests check the pairing holds even when a
limit abandons a scan halfway.

```go
pool := buffer.NewBufferPool(4)
tag := buffer.BufferTag{Path: "movies.data", PageId: 0}
page, _ := pool.Get(tag)   // borrow (miss: loads from disk)
pool.MarkDirty(tag)        // modify in memory, disk untouched
pool.Unpin(tag)            // return (evictable again)
pool.FlushAll()            // manual checkpoint
```

**Read:** [Ch 8](https://www.interdb.jp/pg/pgsql08.html)

**Run:** `go test ./learn/ -run TestBufferSuite -v` ([test](learn/buffer_integration_test.go))

**Tested:** eviction picks victims with a rotating clock hand
(TestClockSweep). Every borrow is paired with Unpin, even when a scan
gives up halfway (TestPoolHeapScanMVCCAbandoned). Dirty pages stay in
memory until flush (TestPoolInsertMVCCFlushDurability).

**Limits:** attached pools flush WAL before pages; detached pools keep the
old behavior and lose dirty pages on crash
(TestPoolInsertMVCCUnflushedLoss). Single-threaded, so no locks. No async
I/O.

### WAL and crash recovery

A crash used to erase changes still in memory. WAL adds a log file next to
the data. Every insert appends a short record. Every commit appends a
marker. The log syncs to disk before the commit counts, so a marked commit
is never lost. Each page stamps its last change. A checkpoint saves changed
pages to disk and records where replay starts. After a crash, recovery
replays from that point and skips records the page already has. Nothing
duplicates, nothing committed goes missing.

```go
seg, _ := wal.Open("movies.wal")
pool := buffer.NewBufferPool(4)
pool.SetWAL(seg)
// ... pooled inserts, flushes ...
wal.Checkpoint(pool, seg, "movies.ctl", mgr.NextID())
// ... crash: drop the pool, reopen the log ...
pool2 := buffer.NewBufferPool(4)
pool2.Recover("movies.data", seg2, "movies.ctl")
```

**Read:** [Ch 9](https://www.interdb.jp/pg/pgsql09.html)

**Run:** `go test ./learn/ -run TestWALSuite -v` ([test](learn/wal_integration_test.go))

**Tested:** log syncs before CLOG marks committed
(TestPoolInsertCommitsToWAL). Replay applies only newer records, skips
flushed ones without duplicating (TestFlushedRecordSkipped,
TestRecoverReplaysUnflushedRow). Checkpoints bound replay to the tail
(TestCrashRecovery).

**Limits:** one data file per recovery (payloads carry no file id). No
abort records: aborted inserts would leave invisible orphans VACUUM must
not freeze yet. No full-page writes, no archiving, no timelines, no
background writer. Crash tests drop the pool in-process; the sync calls
are real but no plug is pulled.

## Project Structure

```
database/
├── executors/            # Query operators (scan, filter, sort, limit, insert)
├── storage/              # Pages, records, heap files, FSM
├── tx/                   # Transactions, commit log (clog)
├── btree/                # B+ tree index (keys + TIDs, linked leaves)
├── vacuum/               # VACUUM, cost tracker, cost reporter
├── buffer/               # Buffer pool (clock sweep, dirty tracking, recovery)
├── wal/                  # Write-ahead log (records, segment, checkpoint)
├── learn/                # Runnable study notes: run with go test ./learn/ -v
├── movies.csv            # Sample data
└── go.mod
```

## TODO

### Query processing ([Ch 3](https://www.interdb.jp/pg/pgsql03.html))
- [ ] Query Planner (rule-based scan selection, cost estimation)
- [ ] JOIN operators (Nested Loop, Hash Join, Sort-Merge Join)

### Concurrency ([Ch 5](https://www.interdb.jp/pg/pgsql05.html))
- [x] MVCC (TxMin/TxMax/CID, clog, snapshots, xip_list, both isolation levels)
- [ ] Write-side concurrency (needs UPDATE/DELETE executors first; then lost-update prevention per [5.8](https://www.interdb.jp/pg/pgsql05/08.html), SELECT FOR UPDATE row locks; SSI, serializable snapshot isolation, dropped as out of scope)

### VACUUM ([Ch 6](https://www.interdb.jp/pg/pgsql06.html))
- [x] Dead tuple detection, freeze processing, index and heap vacuuming
- [x] Cost tracker and reporter (standalone; fires on real pool activity)
- [ ] Throttled VACUUM integration (needs VACUUM reading through the pool)

### Buffer manager ([Ch 8](https://www.interdb.jp/pg/pgsql08.html))
- [x] Buffer pool (page cache, clock-sweep eviction, dirty tracking, manual flush)
- [x] Pooled MVCC scan and insert (separate structs, legacy paths untouched)

### Storage and indexing ([Ch 1](https://www.interdb.jp/pg/pgsql01.html), [Ch 7](https://www.interdb.jp/pg/pgsql07.html))
- [ ] HOT, heap-only tuples (needs UPDATE/DELETE executors, same-page version chaining, skip-index rule)
- [ ] Index-Only Scans (needs a visibility map (VM) with all-visible bits, covering payloads, heap fallback)

### WAL ([Ch 9](https://www.interdb.jp/pg/pgsql09.html))
- [x] Write-Ahead Logging (LSN-stamped pages, commit ordering, checkpoints, replay)
- [ ] Full-page writes (first-change-per-checkpoint page images, torn-write recovery)
- [ ] Multi-file recovery (needs file id in insert payloads)

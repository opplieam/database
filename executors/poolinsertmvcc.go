package executors

import (
	"errors"
	"io"
	"os"

	"database/buffer"
	"database/storage"
	"database/wal"
)

// PoolInsertMVCC inserts one record through the buffer pool.
//
// The record lands in a cached page marked dirty; bytes reach disk on
// eviction or FlushAll, never here. Callers flush at commit for durability.
//
// Page selection scans cached pages for free space, so the FSM sidecar
// file (path.fsm, the per-page free-space map) is left untouched.
//
// ctx and pool are required. Every record stores as a TxRecord, mirroring
// executors.Insert in MVCC mode: Next auto-logs to the clog, and the caller
// still runs mgr.Commit.
//
// Ownership rule: a file is written either through this pooled insert or
// through executors.Insert (straight to disk via storage.InsertRecord),
// never both. executors.Insert on a pooled-written file would trust stale
// FSM numbers.
//
// Example state transitions, inserting into a missing file:
//
// Initial state:
//
//	in.done = false, no file on disk, pool empty
//
// Call 1: Next()
//
//	os.Stat misses -> createFile writes a zero-page file
//	no pages -> appendEmptyPage writes blank page 0 to disk
//	pool.Get(page 0) -> miss, AddRecord in memory, MarkDirty, Unpin
//	return Tuple{1, "Toy Story", "Genre"}
//
// Call 2: Next()
//
//	in.done = true -> return nil, io.EOF
type PoolInsertMVCC struct {
	path   string
	record storage.MovieRecord
	bitmap uint8
	ctx    *TransactionContext
	pool   *buffer.BufferPool
	done   bool
}

// NewPoolInsertMVCC creates a pooled insert for one record.
func NewPoolInsertMVCC(path string, record storage.MovieRecord, bitmap uint8, ctx *TransactionContext, pool *buffer.BufferPool) *PoolInsertMVCC {
	return &PoolInsertMVCC{
		path:   path,
		record: record,
		bitmap: bitmap,
		ctx:    ctx,
		pool:   pool,
	}
}

// Next inserts the record and returns it as a Tuple.
//
// State transitions:
//
//	Call 1: encode TxRecord -> place via pool -> MarkDirty ->
//	        auto-log commit -> done, return Tuple
//	Call 2: done=true -> return io.EOF
func (in *PoolInsertMVCC) Next() (storage.Tuple, error) {
	if in.done {
		return nil, io.EOF
	}
	if in.ctx == nil {
		return nil, errors.New("executors: PoolInsertMVCC requires a non-nil TransactionContext")
	}
	if in.pool == nil {
		return nil, errors.New("executors: PoolInsertMVCC requires a non-nil pool")
	}

	// Step 1: encode the MVCC record.
	cid := in.ctx.Tx.NextCID()
	txRec := storage.InsertTxRecord(in.ctx.Tx.Id(), cid, in.data())
	encoded := storage.EncodeTxRecord(txRec)

	// Step 2: ensure the file exists (empty); placement appends below.
	if _, err := os.Stat(in.path); os.IsNotExist(err) {
		if err := in.createFile(); err != nil {
			return nil, err
		}
	}

	// Step 3: place the bytes in a pooled page, appending if all are full.
	if err := in.placeRecord(encoded); err != nil {
		return nil, err
	}

	// Step 4: commit with write-ahead ordering. The commit record joins
	// the log, the log syncs to disk, and only then does the CLOG mark
	// committed. A crash between sync and mark replays rows that stay
	// invisible: consistent, never committed-and-lost.
	if err := in.appendCommit(); err != nil {
		return nil, err
	}
	if err := in.ctx.Clog.LogCommit(in.ctx.Tx.Id()); err != nil {
		return nil, err
	}

	in.done = true
	return in.data(), nil
}

// spaceNeeded mirrors Page.AddRecord: line pointer plus null bitmap plus bytes.
func spaceNeeded(encoded []byte) int {
	return 4 + 1 + len(encoded)
}

// appendWAL logs the fresh record and stamps the page with its LSN.
// Slot is RecordCount - 1: the row just added. Pools without WAL skip
// silently, keeping legacy behavior with LSN zero.
func (in *PoolInsertMVCC) appendWAL(pid uint32, page *storage.Page, encoded []byte) error {
	seg := in.pool.WAL()
	if seg == nil {
		return nil
	}
	slot := uint16(page.Header.RecordCount - 1)
	rec := &wal.Record{
		Xid:     in.ctx.Tx.Id(),
		Type:    wal.RecordInsert,
		Payload: wal.PayloadInsert(pid, slot, in.bitmap, encoded),
	}
	lsn, err := seg.Append(rec)
	if err != nil {
		return err
	}
	page.Header.LSN = uint64(lsn)
	return nil
}

// appendCommit writes the commit record and flushes the log. Pools
// without WAL skip both, keeping legacy behavior.
func (in *PoolInsertMVCC) appendCommit() error {
	seg := in.pool.WAL()
	if seg == nil {
		return nil
	}
	if _, err := seg.Append(&wal.Record{Xid: in.ctx.Tx.Id(), Type: wal.RecordCommit}); err != nil {
		return err
	}
	return seg.Flush()
}

// createFile makes an empty heap file; placeRecord appends pages to it.
func (in *PoolInsertMVCC) createFile() error {
	f, err := storage.Create(in.path)
	if err != nil {
		return err
	}
	return f.Close()
}

// pageCount reads the page total from the file header.
func pageCount(path string) (uint32, error) {
	f, err := storage.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.PageCount(), nil
}

// appendEmptyPage adds a blank trailing page and returns its id.
func appendEmptyPage(path string) (uint32, error) {
	f, err := storage.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	pid := f.PageCount()
	if err := f.AppendPage(storage.NewPage(pid)); err != nil {
		return 0, err
	}
	return pid, nil
}

// placeRecord writes the bytes to the first pooled page with room,
// appending a fresh page when all are full.
func (in *PoolInsertMVCC) placeRecord(encoded []byte) error {
	need := spaceNeeded(encoded)

	count, err := pageCount(in.path)
	if err != nil {
		return err
	}
	for pid := uint32(0); pid < count; pid++ {
		// Borrow each page in turn; only the fitting one stays changed.
		tag := buffer.BufferTag{Path: in.path, PageId: pid}
		page, err := in.pool.Get(tag)
		if err != nil {
			return err
		}
		if page.GetFreeSpace() >= need {
			if !page.AddRecord(encoded, in.bitmap) {
				_ = in.pool.Unpin(tag)
				return errors.New("executors: record does not fit")
			}
			if err := in.appendWAL(pid, page, encoded); err != nil {
				_ = in.pool.Unpin(tag)
				return err
			}
			if err := in.pool.MarkDirty(tag); err != nil {
				_ = in.pool.Unpin(tag)
				return err
			}
			return in.pool.Unpin(tag)
		}
		if err := in.pool.Unpin(tag); err != nil {
			return err
		}
	}

	// All full (or file empty): append a fresh page, place through the pool.
	pid, err := appendEmptyPage(in.path)
	if err != nil {
		return err
	}
	tag := buffer.BufferTag{Path: in.path, PageId: pid}
	page, err := in.pool.Get(tag)
	if err != nil {
		return err
	}
	if !page.AddRecord(encoded, in.bitmap) {
		_ = in.pool.Unpin(tag)
		return errors.New("executors: record too large for page")
	}
	if err := in.appendWAL(pid, page, encoded); err != nil {
		_ = in.pool.Unpin(tag)
		return err
	}
	if err := in.pool.MarkDirty(tag); err != nil {
		_ = in.pool.Unpin(tag)
		return err
	}
	return in.pool.Unpin(tag)
}

// data returns the tuple data from the record.
func (in *PoolInsertMVCC) data() storage.Tuple {
	return storage.Tuple{in.record.MovieId, in.record.Title, in.record.Genres}
}

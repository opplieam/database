package executors

import (
	"path/filepath"
	"testing"

	"database/buffer"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolInsertMVCCPlacesInMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_write.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	pool := buffer.NewBufferPool(4)
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: wtx.Id(),
		XipList:  wtx.XipList(),
	}

	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	tup, err := ins.Next()
	require.NoError(t, err)
	assert.Equal(t, storage.Tuple{uint32(1), "Toy Story", "Genre"}, tup)

	// Placement cached the page: a miss was recorded, no pins held.
	stats := pool.Stats()
	assert.Equal(t, 1, stats.Misses)
	assert.Equal(t, 0, pool.PinnedCount())

	// Bytes never reached disk: page 0 on disk is still blank.
	// (Commit logging lands in 3b, so visibility is not asserted here.)
	f, err := storage.Open(path)
	require.NoError(t, err)
	raw, err := f.ReadPage(0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, 0, int(raw.Header.RecordCount))
}

func TestPoolInsertMVCCCommittedVisible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_visible.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()
	pool := buffer.NewBufferPool(4)

	// Writer: pooled insert, then manager commit.
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}
	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	_, err = ins.Next()
	require.NoError(t, err)
	mgr.Commit(wtx)

	// Reader in a new transaction sees it through the same pool.
	// No manual clog commit: Next() already logged it.
	scan, err := NewPoolHeapScanMVCC(path, readCtx(mgr, clog), pool)
	require.NoError(t, err)
	result, err := Run(scan)
	require.NoError(t, err)
	require.Equal(t, 1, len(result))
	assert.Equal(t, "Toy Story", result[0][1])
	assert.Equal(t, 0, pool.PinnedCount())
	assert.NoError(t, scan.Close())
}

func TestPoolInsertMVCCFlushDurability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_flush.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	pool := buffer.NewBufferPool(4)
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}
	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	_, err = ins.Next()
	require.NoError(t, err)
	mgr.Commit(wtx)
	require.NoError(t, pool.FlushAll())

	// Fresh pool forces every read to disk: flushed bytes must be there.
	pool2 := buffer.NewBufferPool(4)
	scan, err := NewPoolHeapScanMVCC(path, readCtx(mgr, clog), pool2)
	require.NoError(t, err)
	result, err := Run(scan)
	require.NoError(t, err)
	require.Equal(t, 1, len(result))
	assert.Equal(t, "Toy Story", result[0][1])
	assert.NoError(t, scan.Close())
}

func TestPoolInsertMVCCUnflushedLoss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_loss.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	pool := buffer.NewBufferPool(4)
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}
	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	_, err = ins.Next()
	require.NoError(t, err)
	mgr.Commit(wtx)

	// No flush: a fresh pool reads disk and finds nothing there.
	// This documents no-durability until WAL exists.
	pool2 := buffer.NewBufferPool(4)
	scan, err := NewPoolHeapScanMVCC(path, readCtx(mgr, clog), pool2)
	require.NoError(t, err)
	result, err := Run(scan)
	require.NoError(t, err)
	assert.Equal(t, 0, len(result))
	assert.NoError(t, scan.Close())
}

func TestPoolInsertMVCCGuards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_guards.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	pool := buffer.NewBufferPool(4)
	rec := storage.MovieRecord{MovieId: 1, Title: "Toy Story", Genres: "Genre"}
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}

	_, err = NewPoolInsertMVCC(path, rec, 0, nil, pool).Next()
	assert.Error(t, err)

	_, err = NewPoolInsertMVCC(path, rec, 0, wctx, nil).Next()
	assert.Error(t, err)
}

// TestPoolInsertStampsPageLSN inserts through a WAL-attached pool, then
// proves the page stamp equals the log record's LSN: the link recovery
// compares in step 7.
func TestPoolInsertStampsPageLSN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_wal.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}
	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	_, err = ins.Next()
	require.NoError(t, err)

	// Page holds the record in memory with a nonzero stamp.
	tag := buffer.BufferTag{Path: path, PageId: 0}
	page, err := pool.Get(tag)
	require.NoError(t, err)
	require.Equal(t, 1, int(page.Header.RecordCount))
	assert.NotZero(t, page.Header.LSN)
	stamp := page.Header.LSN
	require.NoError(t, pool.Unpin(tag))

	// The log holds the insert (plus the commit from Next) and the
	// insert's LSN matches the page stamp.
	var lsns []wal.LSN
	var recs []*wal.Record
	require.NoError(t, seg.Iterate(8, func(lsn wal.LSN, rec *wal.Record) error {
		lsns = append(lsns, lsn)
		recs = append(recs, rec)
		return nil
	}))
	require.Len(t, recs, 2)
	assert.Equal(t, wal.RecordInsert, recs[0].Type)
	assert.Equal(t, wal.LSN(stamp), lsns[0])

	// Payload parses to page 0, slot 0, bitmap 0.
	pid, slot, bitmap, _, err := wal.ParseInsert(recs[0].Payload)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), pid)
	assert.Equal(t, uint16(0), slot)
	assert.Equal(t, uint8(0), bitmap)
}

// TestPoolInsertCommitsToWAL inserts through a WAL-attached pool, then
// proves the log holds insert followed by commit, chained and durable
// before the CLOG mark.
func TestPoolInsertCommitsToWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pooled_commit.data")
	clog, err := tx.OpenCommitLog(path + ".clog")
	require.NoError(t, err)
	defer clog.Close()
	mgr := tx.NewTxManager()

	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{Tx: wtx, Clog: clog, Snapshot: wtx.Id(), XipList: wtx.XipList()}
	ins := NewPoolInsertMVCC(path, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	}, 0, wctx, pool)
	_, err = ins.Next()
	require.NoError(t, err)

	// Two records, insert then commit, same transaction, chained.
	var lsns []wal.LSN
	var recs []*wal.Record
	require.NoError(t, seg.Iterate(8, func(lsn wal.LSN, rec *wal.Record) error {
		lsns = append(lsns, lsn)
		recs = append(recs, rec)
		return nil
	}))
	require.Len(t, recs, 2)
	assert.Equal(t, wal.RecordInsert, recs[0].Type)
	assert.Equal(t, wal.RecordCommit, recs[1].Type)
	assert.Equal(t, wtx.Id(), recs[0].Xid)
	assert.Equal(t, wtx.Id(), recs[1].Xid)
	assert.Equal(t, wal.LSN(0), recs[0].PrevLSN)
	assert.Equal(t, lsns[0], recs[1].PrevLSN)

	// CLOG marked committed after the log records.
	assert.True(t, clog.IsCommitted(wtx.Id()))
}

package executors

import (
	"path/filepath"
	"testing"

	"database/buffer"
	"database/storage"
	"database/tx"

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

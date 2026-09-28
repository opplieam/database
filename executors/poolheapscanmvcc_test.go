package executors

import (
	"os"
	"path/filepath"
	"testing"

	"database/buffer"
	"database/storage"
	"database/tx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPooledMVCC writes titles through the Insert executor in one committed
// transaction, returning the clog and manager for reader transactions.
func setupPooledMVCC(t *testing.T, filename string, titles []string) (*tx.CommitLog, *tx.TxManager) {
	t.Helper()
	clog, err := tx.OpenCommitLog(filename + ".clog")
	require.NoError(t, err)

	mgr := tx.NewTxManager()
	wtx := mgr.Begin(tx.RepeatableRead)
	wctx := &TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: wtx.Id(),
		XipList:  wtx.XipList(),
	}
	for i, title := range titles {
		ins := NewInsert(filename, storage.MovieRecord{
			MovieId: uint32(i + 1),
			Title:   title,
			Genres:  "Genre",
		}, 0, wctx)
		_, err := ins.Next()
		require.NoError(t, err)
	}
	mgr.Commit(wtx)
	return clog, mgr
}

// readCtx opens a reader transaction for scanning.
func readCtx(mgr *tx.TxManager, clog *tx.CommitLog) *TransactionContext {
	rtx := mgr.Begin(tx.RepeatableRead)
	return &TransactionContext{
		Tx:       rtx,
		Clog:     clog,
		Snapshot: rtx.Id(),
		XipList:  rtx.XipList(),
	}
}

func TestPoolHeapScanMVCCReuse(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "pooled.data")
	clog, mgr := setupPooledMVCC(t, filename, []string{"Toy Story", "Jumanji"})
	defer clog.Close()

	pool := buffer.NewBufferPool(4)

	// First scan: the page misses once (plus one hit when Next re-checks
	// the exhausted page before advancing).
	scan, err := NewPoolHeapScanMVCC(filename, readCtx(mgr, clog), pool)
	require.NoError(t, err)
	result, err := Run(scan)
	require.NoError(t, err)
	assert.Equal(t, 2, len(result))
	assert.NoError(t, scan.Close())

	// Second scan: every read hits, nothing stays pinned.
	scan2, err := NewPoolHeapScanMVCC(filename, readCtx(mgr, clog), pool)
	require.NoError(t, err)
	result2, err := Run(scan2)
	require.NoError(t, err)
	assert.Equal(t, 2, len(result2))
	assert.NoError(t, scan2.Close())

	stats := pool.Stats()
	assert.Equal(t, 1, stats.Misses)
	assert.Equal(t, 5, stats.Hits)
	assert.Equal(t, 0, pool.PinnedCount())
	assert.Equal(t, "Toy Story", result[0][1])
}

func TestPoolHeapScanMVCCAbandoned(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "pooled_abandoned.data")
	clog, mgr := setupPooledMVCC(t, filename, []string{
		"Toy Story", "Jumanji", "Grumpier Old Men", "Heat",
	})
	defer clog.Close()

	pool := buffer.NewBufferPool(4)
	scan, err := NewPoolHeapScanMVCC(filename, readCtx(mgr, clog), pool)
	require.NoError(t, err)

	// Limit stops pulling after 2 tuples; the scan never finishes.
	limited := NewLimit(scan, 2)
	result, err := Run(limited)
	require.NoError(t, err)
	assert.Equal(t, 2, len(result))

	assert.Equal(t, 0, pool.PinnedCount(), "abandoned scan must hold no pins")
	assert.NoError(t, scan.Close())
}

func TestPoolHeapScanMVCCRequiresCtx(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "pooled_noctx.data")
	clog, _ := setupPooledMVCC(t, filename, []string{"Toy Story"})
	defer clog.Close()
	defer os.Remove(filename + ".fsm")

	pool := buffer.NewBufferPool(4)
	_, err := NewPoolHeapScanMVCC(filename, nil, pool)
	assert.Error(t, err)
}

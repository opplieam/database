package executors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"database/storage"
	"database/tx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeapFileScan(t *testing.T) {
	// Create test file
	movies := []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Adventure"},
		{MovieId: 2, Title: "Jumanji", Genres: "Comedy"},
		{MovieId: 3, Title: "Grumpier Old Men", Genres: "Comedy"},
		{MovieId: 4, Title: "Heat", Genres: "Action"},
	}

	filename := "test_heap_scan.data"
	defer os.Remove(filename)

	err := storage.WriteMoviesPages(filename, movies)
	assert.NoError(t, err)

	// Test 1: Read all records
	scan, err := NewHeapFileScan(filename, nil)
	assert.NoError(t, err)
	defer scan.Close()

	q1 := NewProjection(
		NewLimit(scan, 5),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[1]} },
	)
	result1, err := Run(q1)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(result1), "should read all 4 movies")

	// Test 2: Filter by genre
	scan2, err := NewHeapFileScan(filename, nil)
	assert.NoError(t, err)
	defer scan2.Close()

	q2 := NewSelection(
		scan2,
		func(t storage.Tuple) bool {
			genres := t[2].(string)
			return strings.Contains(genres, "Comedy")
		},
	)
	result2, err := Run(q2)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(result2), "should find 2 Comedy movies")
}

func TestHeapFileScanMVCCSkipsInvisibleWithoutSkippingPage(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "heap_scan_mvcc.data")
	clog, err := tx.OpenCommitLog(filepath.Join(dir, "heap_scan_mvcc.clog"))
	require.NoError(t, err)
	defer clog.Close()

	mgr := tx.NewManager()

	// First record: rollback leaves it invisible.
	tx1 := mgr.Begin(tx.RepeatableRead)
	ctx1 := &TransactionContext{Tx: tx1, Clog: clog, Snapshot: mgr.Snapshot(tx1)}
	first := encodeMVCCInsert(ctx1, storage.Tuple{uint32(1), "Invisible", "Action"})
	require.NoError(t, storage.InsertRecord(filename, first, 0))
	mgr.Rollback(tx1)

	// Second record: commit makes it visible.
	tx2 := mgr.Begin(tx.RepeatableRead)
	ctx2 := &TransactionContext{Tx: tx2, Clog: clog, Snapshot: mgr.Snapshot(tx2)}
	second := encodeMVCCInsert(ctx2, storage.Tuple{uint32(2), "Visible", "Comedy"})
	require.NoError(t, storage.InsertRecord(filename, second, 0))
	require.NoError(t, clog.LogCommit(tx2.ID()))
	mgr.Commit(tx2)

	// Both records must share page 0; otherwise this test proves nothing
	// about skipping within a page.
	f, err := storage.Open(filename)
	require.NoError(t, err)
	require.Equal(t, uint32(1), f.PageCount())
	require.NoError(t, f.Close())

	tx3 := mgr.Begin(tx.RepeatableRead)
	ctx3 := &TransactionContext{Tx: tx3, Clog: clog, Snapshot: mgr.Snapshot(tx3)}
	scan, err := NewHeapFileScan(filename, ctx3)
	require.NoError(t, err)
	defer scan.Close()

	result, err := Run(scan)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Visible", result[0][1])
}

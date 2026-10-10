package buffer

import (
	"path/filepath"
	"testing"

	"database/storage"
	"database/wal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hand-built crash: row A flushed and checkpointed, row B dirty only in
// memory, pool dropped. Fresh pool recovers both rows exactly once.
func TestRecoverReplaysUnflushedRow(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "crash.data")
	ctl := filepath.Join(dir, "test.ctl")

	// One empty page on disk.
	f, err := storage.Create(dataPath)
	require.NoError(t, err)
	require.NoError(t, f.AppendPage(storage.NewPage(0)))
	require.NoError(t, f.Close())

	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)

	rowA := storage.EncodeTxRecord(storage.InsertTxRecord(1, 0, storage.Tuple{uint32(1), "A", "G"}))
	rowB := storage.EncodeTxRecord(storage.InsertTxRecord(1, 1, storage.Tuple{uint32(2), "B", "G"}))

	poolA := NewBufferPool(4)
	poolA.SetWAL(seg)
	tag := BufferTag{Path: dataPath, PageId: 0}

	// Row A: place, log, stamp, flush. Durable before the checkpoint.
	page, err := poolA.Get(tag)
	require.NoError(t, err)
	require.True(t, page.AddRecord(rowA, 0))
	lsnA, err := seg.Append(&wal.Record{Xid: 1, Type: wal.RecordInsert,
		Payload: wal.PayloadInsert(0, 0, 0, rowA)})
	require.NoError(t, err)
	page.Header.LSN = uint64(lsnA)
	require.NoError(t, poolA.MarkDirty(tag))
	require.NoError(t, poolA.Unpin(tag))
	require.NoError(t, poolA.FlushAll())

	// Checkpoint between the rows: replay meets skip-then-apply.
	_, _, err = wal.Checkpoint(poolA, seg, ctl, 2)
	require.NoError(t, err)

	// Row B: place, log, stamp, never flush. The crash takes the pool.
	page, err = poolA.Get(tag)
	require.NoError(t, err)
	require.True(t, page.AddRecord(rowB, 2))
	lsnB, err := seg.Append(&wal.Record{Xid: 1, Type: wal.RecordInsert,
		Payload: wal.PayloadInsert(0, 1, 2, rowB)})
	require.NoError(t, err)
	page.Header.LSN = uint64(lsnB)
	require.NoError(t, poolA.MarkDirty(tag))
	require.NoError(t, poolA.Unpin(tag))
	// No FlushAll: poolA dies dirty. Restart reopens the segment.
	require.NoError(t, seg.Close())

	seg2, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg2.Close()
	poolB := NewBufferPool(4)
	require.NoError(t, poolB.Recover(dataPath, seg2, ctl))

	// Both rows present exactly once, bitmap intact, stamp at row B.
	got, err := poolB.Get(tag)
	require.NoError(t, err)
	require.Equal(t, 2, int(got.Header.RecordCount))
	assert.Equal(t, uint64(lsnB), got.Header.LSN)
	rawA, err := got.GetRecord(0)
	require.NoError(t, err)
	assert.Equal(t, rowA, rawA)
	rawB, err := got.GetRecord(1)
	require.NoError(t, err)
	assert.Equal(t, rowB, rawB)
	nb, err := got.GetNullBitmap(1)
	require.NoError(t, err)
	assert.Equal(t, uint8(2), nb)
	require.NoError(t, poolB.Unpin(tag))
	assert.Equal(t, 0, poolB.PinnedCount())
}

// Unknown start point refuses instead of guessing.
func TestRecoverMissingControl(t *testing.T) {
	dir := t.TempDir()
	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()
	pool := NewBufferPool(4)
	err = pool.Recover(filepath.Join(dir, "nope.data"), seg, filepath.Join(dir, "missing.ctl"))
	assert.Error(t, err)
}

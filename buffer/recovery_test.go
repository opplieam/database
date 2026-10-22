package buffer

import (
	"path/filepath"
	"testing"
	"time"

	"database/storage"
	"database/tx"
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
	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()
	poolB := NewBufferPool(4)
	_, _, err = poolB.Recover(dataPath, seg2, ctl, clog, time.Time{})
	require.NoError(t, err)

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
	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()
	pool := NewBufferPool(4)
	_, _, err = pool.Recover(filepath.Join(dir, "nope.data"), seg, filepath.Join(dir, "missing.ctl"), clog, time.Time{})
	assert.Error(t, err)
}

// A restored backup carries the CLOG file from backup time, so a commit
// made after the backup is missing from it and its row reads back
// invisible (storage.TxRecord.Visible consults the CLOG on every read).
// The WAL is the only surviving copy of that commit, so replay has to
// put the mark back.
func TestRecoverRebuildsClog(t *testing.T) {
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

	poolA := NewBufferPool(4)
	poolA.SetWAL(seg)
	tag := BufferTag{Path: dataPath, PageId: 0}

	// Checkpoint on the empty database: REDO lands at the tip, so both
	// records appended next fall inside the recovery window.
	_, _, err = wal.Checkpoint(poolA, seg, ctl, 1)
	require.NoError(t, err)

	// Transaction 7 writes a row and commits.
	row := storage.EncodeTxRecord(storage.InsertTxRecord(7, 0, storage.Tuple{uint32(1), "A", "G"}))
	page, err := poolA.Get(tag)
	require.NoError(t, err)
	require.True(t, page.AddRecord(row, 0))
	lsnA, err := seg.Append(&wal.Record{Xid: 7, Type: wal.RecordInsert,
		Payload: wal.PayloadInsert(0, 0, 0, row)})
	require.NoError(t, err)
	page.Header.LSN = uint64(lsnA)
	require.NoError(t, poolA.MarkDirty(tag))
	require.NoError(t, poolA.Unpin(tag))
	_, err = seg.Append(&wal.Record{Xid: 7, Type: wal.RecordCommit,
		Payload: wal.PayloadCommit(1)})
	require.NoError(t, err)
	// The pool dies with the row still dirty, and the CLOG the backup
	// carried never saw the commit.
	require.NoError(t, seg.Close())

	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()
	require.False(t, clog.IsCommitted(7), "blank CLOG is the point of the test")

	seg2, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg2.Close()
	poolB := NewBufferPool(4)
	_, _, err = poolB.Recover(dataPath, seg2, ctl, clog, time.Time{})
	require.NoError(t, err)

	assert.True(t, clog.IsCommitted(7), "replay must rebuild the commit mark")

	// That mark is what makes the row readable. A later transaction must
	// see it; before the fix this same call returned false.
	page, err = poolB.Get(tag)
	require.NoError(t, err)
	raw, err := page.GetRecord(0)
	require.NoError(t, err)
	rec, err := storage.DecodeTxRecord(raw)
	require.NoError(t, err)
	assert.True(t, rec.Visible(tx.Snapshot{TxID: 8, Cutoff: 8}, clog), "row must be visible to a later transaction")
	require.NoError(t, poolB.Unpin(tag))
}

// A restore closes the gap between the counter stored at checkpoint time
// and the ids the window spent, so fresh inserts never reuse an id that a
// replayed row already owns.
func TestRecoverAdvancesNextXid(t *testing.T) {
	dir := t.TempDir()
	ctl := filepath.Join(dir, "test.ctl")

	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)

	poolA := NewBufferPool(4)
	poolA.SetWAL(seg)

	// The checkpoint records the counter at 5. No later checkpoint
	// follows, so anything above 5 must come from replayed records.
	_, _, err = wal.Checkpoint(poolA, seg, ctl, 5)
	require.NoError(t, err)

	// Transactions 5 through 9 spend ids inside the window.
	for xid := uint64(5); xid <= 9; xid++ {
		_, err = seg.Append(&wal.Record{Xid: xid, Type: wal.RecordCommit,
			Payload: wal.PayloadCommit(1)})
		require.NoError(t, err)
	}
	require.NoError(t, seg.Close())

	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()

	seg2, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg2.Close()
	poolB := NewBufferPool(4)
	nextXid, _, err := poolB.Recover(filepath.Join(dir, "unused.data"), seg2, ctl, clog, time.Time{})
	require.NoError(t, err)

	// 9 was spent, so the next free id is 10. The checkpoint's 5 is stale.
	assert.Equal(t, uint64(10), nextXid)
}

// The checkpoint record's counter is the floor. Restoring a backup with
// nothing replayed after it must not rewind the counter to zero, or the
// restored cluster starts handing out ids its rows already carry.
func TestRecoverKeepsCheckpointCounter(t *testing.T) {
	dir := t.TempDir()
	ctl := filepath.Join(dir, "test.ctl")

	seg, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)

	poolA := NewBufferPool(4)
	poolA.SetWAL(seg)

	// Checkpoint with the counter at 20, then nothing at all. The window
	// is empty, so the only source for the counter is the checkpoint.
	_, _, err = wal.Checkpoint(poolA, seg, ctl, 20)
	require.NoError(t, err)
	require.NoError(t, seg.Close())

	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()

	seg2, err := wal.Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg2.Close()
	poolB := NewBufferPool(4)
	nextXid, _, err := poolB.Recover(filepath.Join(dir, "unused.data"), seg2, ctl, clog, time.Time{})
	require.NoError(t, err)

	assert.Equal(t, uint64(20), nextXid, "the checkpoint's counter is the floor")
}

// targetBase is the reference the recovery target tests time their commits
// from: transaction n commits at targetBase plus n seconds.
var targetBase = time.Unix(1_700_000_000, 0)

// buildFourCommits writes four single-row transactions one second apart,
// all after the checkpoint, then drops the pool unflushed so a restore has
// to replay them. Returns the paths a restore needs and the commit LSNs,
// so a target test can name which commit replay should stop at.
func buildFourCommits(t *testing.T, dir string) (dataPath, ctl, walPath string, commitLSNs []wal.LSN) {
	t.Helper()
	dataPath = filepath.Join(dir, "crash.data")
	ctl = filepath.Join(dir, "test.ctl")
	walPath = filepath.Join(dir, "test.wal")

	f, err := storage.Create(dataPath)
	require.NoError(t, err)
	require.NoError(t, f.AppendPage(storage.NewPage(0)))
	require.NoError(t, f.Close())

	seg, err := wal.Open(walPath)
	require.NoError(t, err)

	pool := NewBufferPool(4)
	pool.SetWAL(seg)
	tag := BufferTag{Path: dataPath, PageId: 0}

	// Checkpoint first: REDO lands at the tip, so every transaction below
	// falls inside the recovery window.
	_, _, err = wal.Checkpoint(pool, seg, ctl, 1)
	require.NoError(t, err)

	page, err := pool.Get(tag)
	require.NoError(t, err)
	for i := 1; i <= 4; i++ {
		xid := uint64(i)
		slot := uint16(i - 1)
		row := storage.EncodeTxRecord(storage.InsertTxRecord(xid, uint32(i-1),
			storage.Tuple{uint32(i), "Row", "G"}))
		require.True(t, page.AddRecord(row, 0))
		lsn, err := seg.Append(&wal.Record{Xid: xid, Type: wal.RecordInsert,
			Payload: wal.PayloadInsert(0, slot, 0, row)})
		require.NoError(t, err)
		page.Header.LSN = uint64(lsn)
		commitLSN, err := seg.Append(&wal.Record{Xid: xid, Type: wal.RecordCommit,
			Payload: wal.PayloadCommit(targetBase.Add(time.Duration(i) * time.Second).UnixNano())})
		require.NoError(t, err)
		commitLSNs = append(commitLSNs, commitLSN)
	}
	require.NoError(t, pool.MarkDirty(tag))
	require.NoError(t, pool.Unpin(tag))
	require.NoError(t, seg.Close())
	return dataPath, ctl, walPath, commitLSNs
}

// Replay ends at the first commit whose timestamp exceeds the target, so a
// commit landing exactly on the target still survives.
func TestRecoverStopsAtTarget(t *testing.T) {
	cases := []struct {
		name   string
		target time.Time
		want   int // transactions that survive
	}{
		{"before every commit", targetBase, 0},
		{"exactly on the first", targetBase.Add(1 * time.Second), 1},
		{"between the second and third", targetBase.Add(2500 * time.Millisecond), 2},
		{"exactly on the third", targetBase.Add(3 * time.Second), 3},
		{"after every commit", targetBase.Add(10 * time.Second), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dataPath, ctl, walPath, _ := buildFourCommits(t, dir)

			clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
			require.NoError(t, err)
			defer clog.Close()

			seg, err := wal.Open(walPath)
			require.NoError(t, err)
			defer seg.Close()
			pool := NewBufferPool(4)
			_, _, err = pool.Recover(dataPath, seg, ctl, clog, tc.target)
			require.NoError(t, err)

			// Visibility, not row count, is what an operator sees. Each
			// transaction wrote one row, so survivors equal visible rows.
			// Stopped transactions leave their row on the page unmarked.
			tag := BufferTag{Path: dataPath, PageId: 0}
			page, err := pool.Get(tag)
			require.NoError(t, err)
			visible := 0
			for slot := 0; slot < int(page.Header.RecordCount); slot++ {
				raw, err := page.GetRecord(slot)
				require.NoError(t, err)
				rec, err := storage.DecodeTxRecord(raw)
				require.NoError(t, err)
				if rec.Visible(tx.Snapshot{TxID: 99, Cutoff: 99}, clog) {
					visible++
				}
			}
			require.NoError(t, pool.Unpin(tag))
			assert.Equal(t, tc.want, visible)
		})
	}
}

// Replay reports the stop point: the first commit refused as past the
// target, or the log end when nothing was refused. A restore cuts the
// working log there, so skipped history never replays again.
func TestRecoverReportsStopLSN(t *testing.T) {
	dir := t.TempDir()
	dataPath, ctl, walPath, commitLSNs := buildFourCommits(t, dir)
	clog, err := tx.OpenCommitLog(filepath.Join(dir, "test.clog"))
	require.NoError(t, err)
	defer clog.Close()

	// Target between commits two and three: the third commit is the
	// first strictly past it.
	seg, err := wal.Open(walPath)
	require.NoError(t, err)
	pool := NewBufferPool(4)
	_, stopLSN, err := pool.Recover(dataPath, seg, ctl, clog,
		targetBase.Add(2500*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, commitLSNs[2], stopLSN)
	require.NoError(t, seg.Close())

	// Zero target: everything replays and the stop is the log end.
	seg2, err := wal.Open(walPath)
	require.NoError(t, err)
	defer seg2.Close()
	pool2 := NewBufferPool(4)
	_, stopLSN, err = pool2.Recover(dataPath, seg2, ctl, clog, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, seg2.End(), stopLSN)
}

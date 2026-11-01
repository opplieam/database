package replication

import (
	"os"
	"path/filepath"
	"testing"

	"database/backup"
	"database/buffer"
	"database/executors"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// standbyCluster names the paths a standby will occupy. Nothing is
// created: bootstrap copies the files in.
func standbyCluster(dir string) backup.Cluster {
	return backup.Cluster{
		Data:     filepath.Join(dir, "movies.data"),
		Clog:     filepath.Join(dir, "movies.clog"),
		Ctl:      filepath.Join(dir, "movies.ctl"),
		WAL:      filepath.Join(dir, "movies.wal"),
		Archives: filepath.Join(dir, "archive"),
	}
}

// newCluster wires one pool-backed cluster in dir: the same paths
// standbyCluster names, plus the files themselves, with the segment and
// pool wired the way a running primary has them.
func newCluster(t *testing.T, dir string) (backup.Cluster, *buffer.BufferPool,
	*wal.Segment, *tx.CommitLog, *tx.Manager) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))

	c := standbyCluster(dir)
	f, err := storage.Create(c.Data)
	require.NoError(t, err)
	require.NoError(t, f.AppendPage(storage.NewPage(0)))
	require.NoError(t, f.Close())

	seg, err := wal.Open(c.WAL)
	require.NoError(t, err)
	t.Cleanup(func() { seg.Close() })

	clog, err := tx.OpenCommitLog(c.Clog)
	require.NoError(t, err)
	t.Cleanup(func() { clog.Close() })

	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)
	return c, pool, seg, clog, tx.NewManager()
}

// insertPooled writes one row through the pool in its own transaction:
// insert record, commit record, CLOG mark. The real write path.
func insertPooled(t *testing.T, path string, pool *buffer.BufferPool,
	clog *tx.CommitLog, mgr *tx.Manager, m storage.MovieRecord) {
	t.Helper()

	wtx := mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: mgr.Snapshot(wtx),
	}
	ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
	_, err := ins.Next()
	require.NoError(t, err)
	mgr.Commit(wtx)
}

// scanRows reads every visible tuple through a fresh pool, opening the
// cluster's CLOG the way a reader after a restart would.
func scanRows(t *testing.T, c backup.Cluster, mgr *tx.Manager) []storage.Tuple {
	t.Helper()

	clog, err := tx.OpenCommitLog(c.Clog)
	require.NoError(t, err)
	defer clog.Close()

	rtx := mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       rtx,
		Clog:     clog,
		Snapshot: mgr.Snapshot(rtx),
	}
	scan, err := executors.NewPoolHeapScanMVCC(c.Data, ctx, buffer.NewBufferPool(4))
	require.NoError(t, err)
	defer scan.Close()
	rows, err := executors.Run(scan)
	require.NoError(t, err)
	return rows
}

// bootstrapScene builds a primary with Alice and Bob, backs it up, and
// bootstraps a standby from that backup.
func bootstrapScene(t *testing.T) (backup.Cluster, backup.Cluster,
	*buffer.BufferPool, *wal.Segment, *tx.CommitLog, *tx.Manager, *Standby) {
	t.Helper()

	dir := t.TempDir()
	c, pool, seg, clog, mgr := newCluster(t, filepath.Join(dir, "primary"))
	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 1, Title: "Alice", Genres: "Genre",
	})
	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 2, Title: "Bob", Genres: "Genre",
	})

	backupDir := filepath.Join(dir, "backup")
	require.NoError(t, backup.Backup(c, backupDir, pool, seg, mgr.NextID(), mgr.Epoch()))

	sc := standbyCluster(filepath.Join(dir, "standby"))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "standby"), 0755))
	// A standby never archives: it reads the archive it is bootstrapped
	// from, and here that is the primary's on the same disk.
	sc.Archives = c.Archives
	st, err := Bootstrap(sc, backupDir, mgr)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	return c, sc, pool, seg, clog, mgr, st
}

// A standby starts as the backup's state: the rows frozen at backup
// time, readable through its own files.
func TestBootstrapCopiesBackup(t *testing.T) {
	_, sc, _, _, _, mgr, _ := bootstrapScene(t)

	rows := scanRows(t, sc, mgr)
	require.Len(t, rows, 2)
	assert.Equal(t, "Alice", rows[0][1])
	assert.Equal(t, "Bob", rows[1][1])
}

// Without a finished backup there is no standby to start from.
func TestBootstrapRefusesMissingBackup(t *testing.T) {
	dir := t.TempDir()
	_, err := Bootstrap(standbyCluster(filepath.Join(dir, "standby")),
		filepath.Join(dir, "missing"), tx.NewManager())
	assert.Error(t, err)
}

// A commit after bootstrap is invisible to the standby until catch-up
// ships it: the async gap, then closed.
func TestCatchUpAppliesNewRecords(t *testing.T) {
	c, sc, pool, seg, clog, mgr, st := bootstrapScene(t)

	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 3, Title: "Carol", Genres: "Genre",
	})

	// Async: the primary committed, the standby has not received it.
	rows := scanRows(t, sc, mgr)
	require.Len(t, rows, 2, "standby must lag until catch-up runs")

	status, err := st.CatchUp(seg)
	require.NoError(t, err)
	assert.Equal(t, StateCatchUp, status.State)
	assert.Equal(t, seg.End(), status.Sent)
	assert.GreaterOrEqual(t, status.Sent, status.Flushed)
	assert.Equal(t, status.Flushed, status.Applied)

	rows = scanRows(t, sc, mgr)
	require.Len(t, rows, 3)
	assert.Equal(t, "Carol", rows[2][1])
}

// With nothing new to ship the link is streaming, not catching up, and
// a repeated catch-up changes nothing.
func TestCatchUpNoNewRecordsIsStreaming(t *testing.T) {
	c, sc, pool, seg, clog, mgr, st := bootstrapScene(t)

	// Bootstrap copied the backup's log; the primary has appended
	// nothing since, so the first catch-up ships zero bytes.
	status, err := st.CatchUp(seg)
	require.NoError(t, err)
	assert.Equal(t, StateStreaming, status.State)
	assert.Equal(t, status.Sent, status.Flushed)
	assert.Equal(t, status.Flushed, status.Applied)

	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 3, Title: "Carol", Genres: "Genre",
	})

	caught, err := st.CatchUp(seg)
	require.NoError(t, err)
	assert.Equal(t, StateCatchUp, caught.State)

	// One more pass with nothing new: streaming again, positions hold,
	// and the rows stay exactly three (replay did not duplicate).
	again, err := st.CatchUp(seg)
	require.NoError(t, err)
	assert.Equal(t, StateStreaming, again.State)
	assert.Equal(t, caught.Sent, again.Sent)
	assert.Equal(t, caught.Flushed, again.Flushed)
	assert.Equal(t, caught.Applied, again.Applied)

	rows := scanRows(t, sc, mgr)
	require.Len(t, rows, 3)
	assert.Equal(t, "Carol", rows[2][1])
}

// A primary whose log ends before the point the standby already reached
// cannot supply the missing bytes. Send refuses an offset past the end,
// so the link reports it rather than claiming a clean no-op.
func TestCatchUpRefusesPrimaryBehind(t *testing.T) {
	_, _, _, _, _, _, st := bootstrapScene(t)
	from := st.seg.End()
	require.Greater(t, from, wal.LSN(wal.SegmentHeaderSize),
		"the standby must have replayed past its header, or this tests nothing")

	// A fresh segment holds only its header, so its tip sits below the
	// standby's.
	behind, err := wal.Open(filepath.Join(t.TempDir(), "behind.wal"))
	require.NoError(t, err)
	defer behind.Close()

	_, err = st.CatchUp(behind)
	assert.Error(t, err)
}

// A standby pointed at a primary that does not share its past cannot
// splice that log onto its own. Receive validates the whole payload
// before writing any of it, so the refusal leaves the standby's log
// exactly where it was.
//
// Which guard fires depends on where the offset lands: here the bytes
// are not even a record stream the standby can continue, so decodeAt
// rejects them before the chain check would. Both say the same thing,
// that the two logs do not share a past.
func TestCatchUpRefusesUnrelatedPrimary(t *testing.T) {
	_, _, _, _, _, _, st := bootstrapScene(t)
	from := st.seg.End()

	// A second cluster whose own log runs past that offset: the same
	// LSN range as the first cluster's, a different history.
	other, opool, oseg, oclog, omgr := newCluster(t, filepath.Join(t.TempDir(), "other"))
	for i := 0; i < 16 && oseg.End() <= from; i++ {
		insertPooled(t, other.Data, opool, oclog, omgr, storage.MovieRecord{
			MovieId: uint32(i + 100), Title: "Other", Genres: "Genre",
		})
	}
	require.Greater(t, oseg.End(), from, "the second log must reach past the standby's tip")

	_, err := st.CatchUp(oseg)
	assert.Error(t, err)
	assert.Equal(t, from, st.seg.End(), "a refused payload must leave the standby's log untouched")
}

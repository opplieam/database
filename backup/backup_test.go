package backup

import (
	"os"
	"path/filepath"
	"testing"

	"database/buffer"
	"database/executors"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCluster wires one pool-backed cluster in dir: data alongside its
// log, control file, and archive directory, with the segment and pool
// wired through SetWAL the way a running cluster has it.
func newCluster(t *testing.T, dir string) (Cluster, *buffer.BufferPool,
	*wal.Segment, *tx.CommitLog, *tx.TxManager) {
	t.Helper()

	c := Cluster{
		Data:     filepath.Join(dir, "movies.data"),
		Clog:     filepath.Join(dir, "movies.clog"),
		Ctl:      filepath.Join(dir, "movies.ctl"),
		WAL:      filepath.Join(dir, "movies.wal"),
		Archives: filepath.Join(dir, "archive"),
	}
	// The cluster exists before its first row: one empty page on disk,
	// which the pool fills rather than creates.
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
	return c, pool, seg, clog, tx.NewTxManager()
}

// insertPooled writes one row through the pool in its own transaction:
// insert record, commit record, CLOG mark. The real write path.
func insertPooled(t *testing.T, path string, pool *buffer.BufferPool,
	clog *tx.CommitLog, mgr *tx.TxManager, m storage.MovieRecord) {
	t.Helper()

	wtx := mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: wtx.Id(),
		XipList:  wtx.XipList(),
	}
	ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
	_, err := ins.Next()
	require.NoError(t, err)
	mgr.Commit(wtx)
}

// A backup is a snapshot in a directory: rows written later change the
// live cluster, never the backup. The label agrees with the checkpoint
// and names the archive the restore will read.
func TestBackupSnapshotIsFrozen(t *testing.T) {
	dir := t.TempDir()
	c, pool, seg, clog, mgr := newCluster(t, dir)
	outDir := filepath.Join(dir, "backup")

	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 1, Title: "Toy Story", Genres: "Genre",
	})

	// The label's REDO must equal the log tip before the call: the
	// checkpoint inside Backup captures the end as REDO and nothing
	// appends in between.
	tip := seg.End()
	require.NoError(t, Backup(c, outDir, pool, seg, mgr.NextID()))
	label, err := wal.ReadBackupLabel(filepath.Join(outDir, "backup_label"))
	require.NoError(t, err)
	assert.Equal(t, tip, label.RedoLSN)
	assert.Equal(t, mgr.NextID(), label.NextXid)

	// The archive it names exists and is the newest one.
	seq, err := wal.LatestArchive(c.Archives)
	require.NoError(t, err)
	assert.Equal(t, label.ArchiveSeq, seq, "label must name the newest archive")
	assert.FileExists(t, wal.ArchivePath(c.Archives, seq))

	// The copied control file names the same REDO as the label: both
	// come from the one Checkpoint call.
	_, ctlRedo, err := wal.ReadControl(filepath.Join(outDir, filepath.Base(c.Ctl)))
	require.NoError(t, err)
	assert.Equal(t, label.RedoLSN, ctlRedo)

	// Rows after the backup land in the live file only.
	insertPooled(t, c.Data, pool, clog, mgr, storage.MovieRecord{
		MovieId: 2, Title: "Jumanji", Genres: "Genre",
	})
	require.NoError(t, pool.FlushAll())

	live, err := storage.Open(c.Data)
	require.NoError(t, err)
	page, err := live.ReadPage(0)
	require.NoError(t, err)
	require.NoError(t, live.Close())
	assert.Equal(t, 2, int(page.Header.RecordCount), "live file must hold both rows")

	snap, err := storage.Open(filepath.Join(outDir, filepath.Base(c.Data)))
	require.NoError(t, err)
	page, err = snap.ReadPage(0)
	require.NoError(t, err)
	require.NoError(t, snap.Close())
	assert.Equal(t, 1, int(page.Header.RecordCount), "backup must still hold one row")
}

// The label marks a completed backup, so a second backup into the same
// directory refuses instead of interleaving two backups' files.
func TestBackupRefusesFinishedOutDir(t *testing.T) {
	dir := t.TempDir()
	c, pool, seg, _, mgr := newCluster(t, dir)
	outDir := filepath.Join(dir, "backup")

	require.NoError(t, Backup(c, outDir, pool, seg, mgr.NextID()))
	want, err := wal.ReadBackupLabel(filepath.Join(outDir, "backup_label"))
	require.NoError(t, err)

	assert.Error(t, Backup(c, outDir, pool, seg, mgr.NextID()))

	got, err := wal.ReadBackupLabel(filepath.Join(outDir, "backup_label"))
	require.NoError(t, err)
	assert.Equal(t, want, got, "refused write must not disturb the first backup")
}

// A directory with leftovers but no label is not a backup: a retry
// after a failed copy proceeds and overwrites.
func TestBackupAllowsPartialOutDir(t *testing.T) {
	dir := t.TempDir()
	c, pool, seg, _, mgr := newCluster(t, dir)
	outDir := filepath.Join(dir, "backup")
	require.NoError(t, os.MkdirAll(outDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "stale"), []byte("x"), 0644))

	require.NoError(t, Backup(c, outDir, pool, seg, mgr.NextID()))
	_, err := wal.ReadBackupLabel(filepath.Join(outDir, "backup_label"))
	require.NoError(t, err)
}

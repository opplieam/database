package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRestoreCluster(t *testing.T, dir string) Cluster {
	t.Helper()
	return Cluster{
		Data:     filepath.Join(dir, "movies.data"),
		Clog:     filepath.Join(dir, "movies.clog"),
		Ctl:      filepath.Join(dir, "movies.ctl"),
		WAL:      filepath.Join(dir, "movies.wal"),
		Archives: filepath.Join(dir, "archive"),
	}
}

// A restore without a label has no start point: refuse, never guess.
// ReadBackupLabel already rejects short, wrongly stamped, and
// wrong-version files; here the missing file and a corrupt one both
// arrive bad.
func TestRestoreRefusesWithoutLabel(t *testing.T) {
	dir := t.TempDir()
	c := newRestoreCluster(t, dir)
	backupDir := filepath.Join(dir, "backup")

	assert.Error(t, Restore(c, backupDir, time.Time{}, tx.NewTxManager()))

	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "backup_label"),
		[]byte("garbage"), 0644))
	assert.Error(t, Restore(c, backupDir, time.Time{}, tx.NewTxManager()))
}

// The label names the archive the log must come from. When the newest
// archive falls behind that demand, the restore refuses instead of
// replaying a partial window.
func TestRestoreRefusesLostArchive(t *testing.T) {
	dir := t.TempDir()
	c := newRestoreCluster(t, dir)
	backupDir := filepath.Join(dir, "backup")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, wal.WriteBackupLabel(filepath.Join(backupDir, "backup_label"),
		wal.BackupLabel{RedoLSN: 8, NextXid: 1, ArchiveSeq: 1}))

	assert.Error(t, Restore(c, backupDir, time.Time{}, tx.NewTxManager()))
}

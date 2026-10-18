package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three fields survive a trip through disk, and the file is exactly
// the documented 32 bytes.
func TestBackupLabelRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup_label")
	want := BackupLabel{RedoLSN: 4096, NextXid: 7, ArchiveSeq: 3}

	require.NoError(t, WriteBackupLabel(path, want))

	got, err := ReadBackupLabel(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	buf, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Len(t, buf, BackupLabelSize)
	assert.Equal(t, "BKP1", string(buf[:4]))
}

// A label is replaced, not appended: a second backup into the same
// directory takes over the name, and the tmp file does not survive.
func TestBackupLabelReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup_label")
	require.NoError(t, WriteBackupLabel(path, BackupLabel{RedoLSN: 4096, NextXid: 7, ArchiveSeq: 3}))

	second := BackupLabel{RedoLSN: 8192, NextXid: 9, ArchiveSeq: 4}
	require.NoError(t, WriteBackupLabel(path, second))

	got, err := ReadBackupLabel(path)
	require.NoError(t, err)
	assert.Equal(t, second, got)
	assert.NoFileExists(t, path+".tmp")
}

// A missing, short, wrongly stamped, or wrong-version label refuses.
func TestBackupLabelRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	require.NoError(t, WriteBackupLabel(good, BackupLabel{RedoLSN: 4096, NextXid: 7, ArchiveSeq: 3}))
	full, err := os.ReadFile(good)
	require.NoError(t, err)

	short := append([]byte{}, full...)
	short = short[:len(short)-1]
	badMagic := append([]byte{}, full...)
	badMagic[0] = 'X'
	badVersion := append([]byte{}, full...)
	badVersion[4] = 9

	for name, buf := range map[string][]byte{
		"short":       short,
		"bad_magic":   badMagic,
		"bad_version": badVersion,
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, buf, 0644))
		_, err := ReadBackupLabel(path)
		assert.Error(t, err, name)
	}

	_, err = ReadBackupLabel(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Archive freezes the log as it stood. Appends after the archive reach
// the live log and never the archive.
func TestArchiveSnapshot(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")
	archiveDir := filepath.Join(dir, "archive")

	seg, err := Open(walPath)
	require.NoError(t, err)
	defer seg.Close()

	_, err = seg.Append(&Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})})
	require.NoError(t, err)
	_, err = seg.Append(&Record{Xid: 1, Type: RecordCommit, Payload: PayloadCommit(100)})
	require.NoError(t, err)

	frozen := seg.End()
	seq, err := seg.Archive(archiveDir)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), seq)

	_, err = seg.Append(&Record{Xid: 2, Type: RecordCommit, Payload: PayloadCommit(200)})
	require.NoError(t, err)
	assert.Greater(t, seg.End(), frozen)

	live, err := os.ReadFile(walPath)
	require.NoError(t, err)
	archived, err := os.ReadFile(ArchivePath(archiveDir, 1))
	require.NoError(t, err)

	// The archive is the live log cut at the frozen end, no more.
	assert.Len(t, archived, int(frozen))
	assert.Equal(t, live[:int(frozen)], archived)
	assert.Len(t, live, int(seg.End()))
}

// Each archive takes the next sequence number, and a missing archive
// directory reads as zero.
func TestArchiveNumbering(t *testing.T) {
	dir := t.TempDir()
	archiveDir := filepath.Join(dir, "archive")

	missing, err := LatestArchive(archiveDir)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), missing)

	seg, err := Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	for want := uint64(1); want <= 3; want++ {
		_, err := seg.Append(&Record{Xid: want, Type: RecordCommit, Payload: PayloadCommit(int64(want))})
		require.NoError(t, err)

		got, err := seg.Archive(archiveDir)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	latest, err := LatestArchive(archiveDir)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), latest)

	// Nothing was overwritten: each archive holds one more record than
	// the one before it.
	var prev int64
	for seq := uint64(1); seq <= 3; seq++ {
		st, err := os.Stat(ArchivePath(archiveDir, seq))
		require.NoError(t, err)
		assert.Greater(t, st.Size(), prev)
		prev = st.Size()
	}
}

// Open truncates a torn tail, so the archive stops at the last good
// record instead of copying half of one.
func TestArchiveDropsTornTail(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "test.wal")
	archiveDir := filepath.Join(dir, "archive")

	seg, err := Open(walPath)
	require.NoError(t, err)
	_, err = seg.Append(&Record{Xid: 1, Type: RecordCommit, Payload: PayloadCommit(100)})
	require.NoError(t, err)
	second, err := seg.Append(&Record{Xid: 2, Type: RecordCommit, Payload: PayloadCommit(200)})
	require.NoError(t, err)
	require.NoError(t, seg.Close())

	// Simulate a crash mid-append: cut one byte into the last record.
	st, err := os.Stat(walPath)
	require.NoError(t, err)
	require.NoError(t, os.Truncate(walPath, st.Size()-1))

	seg, err = Open(walPath)
	require.NoError(t, err)
	defer seg.Close()
	assert.Equal(t, second, seg.End())

	seq, err := seg.Archive(archiveDir)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), seq)

	live, err := os.ReadFile(walPath)
	require.NoError(t, err)
	archived, err := os.ReadFile(ArchivePath(archiveDir, 1))
	require.NoError(t, err)

	assert.Len(t, archived, int(second))
	assert.Equal(t, live, archived)
}

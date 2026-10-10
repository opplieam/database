package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFlusher records Checkpoint's flush call without importing buffer
// (which would cycle: buffer already imports wal).
type fakeFlusher struct{ flushed int }

func (f *fakeFlusher) FlushAll() error {
	f.flushed++
	return nil
}

// Checkpoint publishes equal REDO and checkpoint LSNs, flushes once,
// and chains the checkpoint record at the log tip.
func TestCheckpoint(t *testing.T) {
	dir := t.TempDir()
	seg, err := Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()
	ctl := filepath.Join(dir, "test.ctl")

	lsn1, err := seg.Append(&Record{Xid: 1, Type: RecordCommit})
	require.NoError(t, err)

	flusher := &fakeFlusher{}
	checkpoint, redo, err := Checkpoint(flusher, seg, ctl, 9)
	require.NoError(t, err)

	// Single-threaded: nothing appends between capture and write.
	assert.Equal(t, checkpoint, redo)
	assert.Equal(t, 1, flusher.flushed)

	// Control file round-trips both values.
	gotCheckpoint, gotRedo, err := ReadControl(ctl)
	require.NoError(t, err)
	assert.Equal(t, checkpoint, gotCheckpoint)
	assert.Equal(t, redo, gotRedo)

	// Tail of the log is the checkpoint record, chained behind the commit.
	var tail []*Record
	require.NoError(t, seg.Iterate(lsn1, func(_ LSN, rec *Record) error {
		tail = append(tail, rec)
		return nil
	}))
	require.Len(t, tail, 2)
	assert.Equal(t, RecordCheckpoint, tail[1].Type)
	assert.Equal(t, lsn1, tail[1].PrevLSN)
	gotRedo, gotNext, err := ParseCheckpoint(tail[1].Payload)
	require.NoError(t, err)
	assert.Equal(t, redo, gotRedo)
	assert.Equal(t, uint64(9), gotNext)

	// A second round advances both LSNs past the first checkpoint.
	_, err = seg.Append(&Record{Xid: 2, Type: RecordCommit})
	require.NoError(t, err)
	checkpoint2, redo2, err := Checkpoint(flusher, seg, ctl, 10)
	require.NoError(t, err)
	assert.Greater(t, checkpoint2, checkpoint)
	assert.Equal(t, checkpoint2, redo2)
}

// Missing or corrupt control files refuse instead of guessing.
func TestReadControlErrors(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ReadControl(filepath.Join(dir, "missing.ctl"))
	assert.Error(t, err)

	bad := filepath.Join(dir, "bad.ctl")
	require.NoError(t, os.WriteFile(bad, []byte("garbage garbage garbage!"), 0644))
	_, _, err = ReadControl(bad)
	assert.Error(t, err)
}

package wal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collect drains Iterate into LSN-ordered slices for assertions.
func collect(t *testing.T, seg *Segment, from LSN) ([]LSN, []*Record) {
	t.Helper()
	var lsns []LSN
	var recs []*Record
	err := seg.Iterate(from, func(lsn LSN, rec *Record) error {
		lsns = append(lsns, lsn)
		recs = append(recs, rec)
		return nil
	})
	require.NoError(t, err)
	return lsns, recs
}

// Append three records: LSNs chain from 8, prev-links chain behind.
func TestAppendIterate(t *testing.T) {
	seg, err := Open(filepath.Join(t.TempDir(), "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	r1 := &Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})}
	r2 := &Record{Xid: 1, Type: RecordCommit}
	r3 := &Record{Xid: 2, Type: RecordInsert, Payload: PayloadInsert(0, 1, 0, []byte{2, 3})}

	lsn1, err := seg.Append(r1)
	require.NoError(t, err)
	lsn2, err := seg.Append(r2)
	require.NoError(t, err)
	lsn3, err := seg.Append(r3)
	require.NoError(t, err)

	assert.Equal(t, LSN(8), lsn1)
	assert.Equal(t, lsn1+LSN(len(EncodeRecord(r1))), lsn2)
	assert.Equal(t, lsn2+LSN(len(EncodeRecord(r2))), lsn3)
	assert.Equal(t, LSN(0), r1.PrevLSN)
	assert.Equal(t, lsn1, r2.PrevLSN)
	assert.Equal(t, lsn2, r3.PrevLSN)

	// Full read preserves order and LSNs.
	lsns, recs := collect(t, seg, 8)
	require.Len(t, recs, 3)
	assert.Equal(t, []LSN{lsn1, lsn2, lsn3}, lsns)
	assert.Equal(t, RecordInsert, recs[0].Type)
	assert.Equal(t, RecordCommit, recs[1].Type)

	// Mid-file start returns only the tail.
	lsns, recs = collect(t, seg, lsn2)
	require.Len(t, recs, 2)
	assert.Equal(t, []LSN{lsn2, lsn3}, lsns)
}

// Truncate one byte into the last record: reopen truncates the torn tail,
// iteration yields the good records, and the next append reuses the end.
func TestTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	seg, err := Open(path)
	require.NoError(t, err)

	_, err = seg.Append(&Record{Xid: 1, Type: RecordCommit})
	require.NoError(t, err)
	lsn2, err := seg.Append(&Record{Xid: 2, Type: RecordCommit})
	require.NoError(t, err)
	require.NoError(t, seg.Close())

	// Simulate a crash mid-append: cut one byte into the last record.
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, os.Truncate(path, st.Size()-1))

	seg, err = Open(path)
	require.NoError(t, err)
	defer seg.Close()

	_, recs := collect(t, seg, 8)
	require.Len(t, recs, 1)
	assert.Equal(t, uint64(1), recs[0].Xid)

	lsn3, err := seg.Append(&Record{Xid: 3, Type: RecordCommit})
	require.NoError(t, err)
	assert.Equal(t, lsn2, lsn3) // truncated end reused, no gap
}

// Close and reopen resumes appends at the prior end.
func TestResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	seg, err := Open(path)
	require.NoError(t, err)
	lsn1, err := seg.Append(&Record{Xid: 1, Type: RecordCommit})
	require.NoError(t, err)
	require.NoError(t, seg.Close())

	seg, err = Open(path)
	require.NoError(t, err)
	defer seg.Close()
	lsn2, err := seg.Append(&Record{Xid: 2, Type: RecordCommit})
	require.NoError(t, err)
	assert.Greater(t, lsn2, lsn1)

	_, recs := collect(t, seg, 8)
	require.Len(t, recs, 2)
}

// A non-WAL file fails open with a clear error.
func TestBadMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	require.NoError(t, os.WriteFile(path, []byte("not a wal file........"), 0644))
	_, err := Open(path)
	assert.Error(t, err)
}

package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustAppend appends one record and returns its LSN.
func mustAppend(t *testing.T, seg *Segment, rec *Record) LSN {
	t.Helper()
	lsn, err := seg.Append(rec)
	require.NoError(t, err)
	return lsn
}

// Send copies [from, end) byte for byte and reports the tip it sent.
func TestSendRange(t *testing.T) {
	seg, err := Open(filepath.Join(t.TempDir(), "src.wal"))
	require.NoError(t, err)
	defer seg.Close()

	mustAppend(t, seg, &Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})})
	r2 := &Record{Xid: 2, Type: RecordCommit}
	lsn2 := mustAppend(t, seg, r2)

	var buf bytes.Buffer
	end, err := seg.Send(&buf, lsn2)
	require.NoError(t, err)
	assert.Equal(t, seg.End(), end)
	assert.Equal(t, EncodeRecord(r2), buf.Bytes())
}

// A send from the tip moves nothing and succeeds: no records behind it.
func TestSendFromEnd(t *testing.T) {
	seg, err := Open(filepath.Join(t.TempDir(), "src.wal"))
	require.NoError(t, err)
	defer seg.Close()
	mustAppend(t, seg, &Record{Xid: 1, Type: RecordCommit})

	var buf bytes.Buffer
	end, err := seg.Send(&buf, seg.End())
	require.NoError(t, err)
	assert.Equal(t, seg.End(), end)
	assert.Empty(t, buf.Bytes())
}

// Nothing exists below the header or past the tip: both refuse.
func TestSendRefusesBounds(t *testing.T) {
	seg, err := Open(filepath.Join(t.TempDir(), "src.wal"))
	require.NoError(t, err)
	defer seg.Close()
	mustAppend(t, seg, &Record{Xid: 1, Type: RecordCommit})

	var buf bytes.Buffer
	_, err = seg.Send(&buf, 0)
	assert.Error(t, err)
	_, err = seg.Send(&buf, SegmentHeaderSize-1)
	assert.Error(t, err)
	_, err = seg.Send(&buf, seg.End()+1)
	assert.Error(t, err)
}

// A shipped range lands whole: same LSNs, same records, and a later
// local append chains onto the received last record.
func TestReceiveAppends(t *testing.T) {
	src, err := Open(filepath.Join(t.TempDir(), "src.wal"))
	require.NoError(t, err)
	defer src.Close()

	mustAppend(t, src, &Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})})
	lsn2 := mustAppend(t, src, &Record{Xid: 2, Type: RecordCommit})

	var buf bytes.Buffer
	_, err = src.Send(&buf, SegmentHeaderSize)
	require.NoError(t, err)

	dst, err := Open(filepath.Join(t.TempDir(), "dst.wal"))
	require.NoError(t, err)
	defer dst.Close()

	tip, err := dst.Receive(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, src.End(), tip)

	lsns, recs := collect(t, dst, SegmentHeaderSize)
	require.Len(t, recs, 2)
	assert.Equal(t, []LSN{SegmentHeaderSize, lsn2}, lsns)
	assert.Equal(t, uint64(1), recs[0].Xid)

	next := &Record{Xid: 3, Type: RecordCommit}
	lsn3 := mustAppend(t, dst, next)
	assert.Equal(t, tip, lsn3)
	assert.Equal(t, lsn2, next.PrevLSN)
}

// An empty payload changes nothing and is not an error.
func TestReceiveEmptyIsNoop(t *testing.T) {
	dst, err := Open(filepath.Join(t.TempDir(), "dst.wal"))
	require.NoError(t, err)
	defer dst.Close()

	tip, err := dst.Receive(nil)
	require.NoError(t, err)
	assert.Equal(t, LSN(SegmentHeaderSize), tip)
	assert.Equal(t, LSN(SegmentHeaderSize), dst.End())
}

// Undecodable bytes are refused as a whole: the segment's tip and
// bytes stay exactly as they were.
func TestReceiveRefusesGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dst.wal")
	dst, err := Open(path)
	require.NoError(t, err)
	defer dst.Close()
	mustAppend(t, dst, &Record{Xid: 1, Type: RecordCommit})

	before := dst.End()
	st, err := os.Stat(path)
	require.NoError(t, err)

	_, err = dst.Receive([]byte("not a wal record"))
	assert.Error(t, err)

	assert.Equal(t, before, dst.End())
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, st.Size(), after.Size())

	_, recs := collect(t, dst, SegmentHeaderSize)
	require.Len(t, recs, 1)
}

// A range that does not continue the receiver's last record is refused:
// the two logs do not share a past, so joining them would corrupt one.
func TestReceiveRefusesBrokenChain(t *testing.T) {
	src, err := Open(filepath.Join(t.TempDir(), "src.wal"))
	require.NoError(t, err)
	defer src.Close()
	mustAppend(t, src, &Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})})
	mustAppend(t, src, &Record{Xid: 2, Type: RecordCommit})

	var buf bytes.Buffer
	_, err = src.Send(&buf, SegmentHeaderSize)
	require.NoError(t, err)

	// The receiver already holds its own history, so the sender's first
	// record (chained to 0) cannot attach here.
	dst, err := Open(filepath.Join(t.TempDir(), "dst.wal"))
	require.NoError(t, err)
	defer dst.Close()
	mustAppend(t, dst, &Record{Xid: 9, Type: RecordCommit})

	before := dst.End()
	_, err = dst.Receive(buf.Bytes())
	assert.Error(t, err)
	assert.Equal(t, before, dst.End())
}

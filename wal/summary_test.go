package wal

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Inserts across pages 0, 1, 0, 2 with a commit between, so the
// summarize walk must deduplicate, sort, and skip non-insert records.
func TestSummarizeCollectsPages(t *testing.T) {
	dir := t.TempDir()
	seg, err := Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	for _, rec := range []*Record{
		{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1})},
		{Xid: 1, Type: RecordCommit},
		{Xid: 2, Type: RecordInsert, Payload: PayloadInsert(1, 0, 0, []byte{2})},
		{Xid: 3, Type: RecordInsert, Payload: PayloadInsert(0, 1, 0, []byte{3})},
		{Xid: 4, Type: RecordInsert, Payload: PayloadInsert(2, 0, 0, []byte{4})},
	} {
		_, err := seg.Append(rec)
		require.NoError(t, err)
	}

	dataPath := filepath.Join(dir, "cluster.data")
	require.NoError(t, os.WriteFile(dataPath, make([]byte, 2*storage.PageSize), 0644))

	sum, err := Summarize(seg, dataPath, SegmentHeaderSize, seg.End())
	require.NoError(t, err)
	assert.Equal(t, []uint32{0, 1, 2}, sum.Pages)
}

// The range echoes back as written, PageCount is the data file's length
// in pages, a shorter file rounds down, and a missing data file refuses
// rather than writing a summary whose count is a guess.
func TestSummarizeReportsBoundsAndCount(t *testing.T) {
	dir := t.TempDir()
	seg, err := Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	_, err = seg.Append(&Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(1, 0, 0, []byte{1})})
	require.NoError(t, err)

	dataPath := filepath.Join(dir, "cluster.data")
	require.NoError(t, os.WriteFile(dataPath, make([]byte, 2*storage.PageSize), 0644))

	from, to := LSN(SegmentHeaderSize), seg.End()
	sum, err := Summarize(seg, dataPath, from, to)
	require.NoError(t, err)
	assert.Equal(t, from, sum.StartLSN)
	assert.Equal(t, to, sum.EndLSN)
	assert.Equal(t, uint32(2), sum.PageCount)

	// A file shorter than one page reads as zero pages, rounding down.
	short := filepath.Join(dir, "short.data")
	require.NoError(t, os.WriteFile(short, make([]byte, 100), 0644))
	sum, err = Summarize(seg, short, from, to)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), sum.PageCount)

	// No writes between the two LSNs: an empty but valid summary.
	sum, err = Summarize(seg, dataPath, to, to)
	require.NoError(t, err)
	assert.Empty(t, sum.Pages)

	sum, err = Summarize(seg, filepath.Join(dir, "absent.data"), from, to)
	assert.Error(t, err)
	assert.Equal(t, Summary{}, sum)
}

// A summary survives disk: same range name, same fields read back.
func TestSummaryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Summary{StartLSN: 100, EndLSN: 280, PageCount: 1, Pages: []uint32{0, 3}}
	require.NoError(t, WriteSummary(dir, in))

	path := SummaryPath(dir, 100, 280)
	assert.FileExists(t, path)

	out, err := ReadSummary(path)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

// Adjacent ranges share an LSN boundary, and each reports only the
// pages its own stretch touched, even when a page changed in both:
// the union is the reader's job, not the summarizer's.
func TestSecondSummarizeOnlyNewerPages(t *testing.T) {
	dir := t.TempDir()
	seg, err := Open(filepath.Join(dir, "test.wal"))
	require.NoError(t, err)
	defer seg.Close()

	for _, page := range []uint32{0, 1} {
		_, err := seg.Append(&Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(page, 0, 0, []byte{1})})
		require.NoError(t, err)
	}
	mid := seg.End()

	for _, page := range []uint32{1, 2} {
		_, err := seg.Append(&Record{Xid: 2, Type: RecordInsert, Payload: PayloadInsert(page, 1, 0, []byte{2})})
		require.NoError(t, err)
	}

	dataPath := filepath.Join(dir, "cluster.data")
	require.NoError(t, os.WriteFile(dataPath, make([]byte, 3*storage.PageSize), 0644))

	first, err := Summarize(seg, dataPath, SegmentHeaderSize, mid)
	require.NoError(t, err)
	assert.Equal(t, []uint32{0, 1}, first.Pages)

	second, err := Summarize(seg, dataPath, mid, seg.End())
	require.NoError(t, err)
	// Page 1 reappears here under its own range: each summary reports
	// only what its stretch saw, stateless between calls, so nothing
	// future leaks into the first, and unioning the ranges is the
	// reader's job, which sets up step 9.
	assert.Equal(t, []uint32{1, 2}, second.Pages)
}

// Missing, short, wrongly stamped, or wrong-version files refuse with a
// zero summary, matching the control file and label readers.
func TestReadSummaryRefuses(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadSummary(filepath.Join(dir, "absent.summary"))
	assert.Error(t, err)

	short := filepath.Join(dir, "short.summary")
	require.NoError(t, os.WriteFile(short, make([]byte, 10), 0644))
	_, err = ReadSummary(short)
	assert.Error(t, err)

	bad := filepath.Join(dir, "bad.summary")
	require.NoError(t, os.WriteFile(bad, []byte("not a summary file......"), 0644))
	_, err = ReadSummary(bad)
	assert.Error(t, err)

	version := filepath.Join(dir, "version.summary")
	buf := make([]byte, 28)
	copy(buf, summaryStamp)
	binary.LittleEndian.PutUint32(buf[4:], 9)
	require.NoError(t, os.WriteFile(version, buf, 0644))
	_, err = ReadSummary(version)
	assert.Error(t, err)
}

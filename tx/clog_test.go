package tx

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitLog(t *testing.T) {
	clogFile := "test_clog.data"
	defer os.Remove(clogFile)

	clog, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)
	defer clog.Close()

	// Unknown tx is in progress
	assert.True(t, clog.IsInProgress(999))
	assert.False(t, clog.IsCommitted(999))
	assert.False(t, clog.IsAborted(999))

	// Log commit
	err = clog.LogCommit(1)
	assert.NoError(t, err)
	assert.True(t, clog.IsCommitted(1))
	assert.False(t, clog.IsAborted(1))
	assert.False(t, clog.IsInProgress(1))

	// Log abort
	err = clog.LogAbort(2)
	assert.NoError(t, err)
	assert.False(t, clog.IsCommitted(2))
	assert.True(t, clog.IsAborted(2))
	assert.False(t, clog.IsInProgress(2))
}

func TestCommitLogPersistence(t *testing.T) {
	clogFile := "test_clog_persist.data"
	defer os.Remove(clogFile)

	// Write commits
	clog1, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)

	require.NoError(t, clog1.LogCommit(10))
	require.NoError(t, clog1.LogAbort(20))
	require.NoError(t, clog1.LogCommit(30))
	require.NoError(t, clog1.Close())

	// Reopen and verify
	clog2, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)
	defer clog2.Close()

	assert.True(t, clog2.IsCommitted(10))
	assert.True(t, clog2.IsAborted(20))
	assert.True(t, clog2.IsCommitted(30))
	assert.False(t, clog2.IsCommitted(999))
}

func TestCommitLogTornTail(t *testing.T) {
	clogFile := "test_clog_torn.data"
	defer os.Remove(clogFile)

	// One complete record (txID 1, committed), then 4 stray bytes as if a
	// crash cut the next append short.
	require.NoError(t, os.WriteFile(clogFile,
		[]byte{1, 0, 0, 0, 0, 0, 0, 0, 1, 0xAA, 0xBB, 0xCC, 0xDD}, 0644))

	clog, err := OpenCommitLog(clogFile)
	require.NoError(t, err)
	assert.True(t, clog.IsCommitted(1))

	st, err := os.Stat(clogFile)
	require.NoError(t, err)
	assert.Equal(t, int64(clogRecordSize), st.Size(), "torn tail is truncated on open")

	// Appends must still land on a record boundary.
	require.NoError(t, clog.LogAbort(2))
	require.NoError(t, clog.Close())

	clog2, err := OpenCommitLog(clogFile)
	require.NoError(t, err)
	defer clog2.Close()
	assert.True(t, clog2.IsCommitted(1))
	assert.True(t, clog2.IsAborted(2))
}

func TestCommitLogOverwrite(t *testing.T) {
	clogFile := "test_clog_overwrite.data"
	defer os.Remove(clogFile)

	clog, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)
	defer clog.Close()

	// Commit tx=1
	require.NoError(t, clog.LogCommit(1))
	assert.True(t, clog.IsCommitted(1))

	// Abort tx=1 (overwrite)
	require.NoError(t, clog.LogAbort(1))
	assert.False(t, clog.IsCommitted(1))
	assert.True(t, clog.IsAborted(1))
}

func TestCommitLogPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clog")
	cl, err := OpenCommitLog(path)
	require.NoError(t, err)

	for _, id := range []uint64{1, 5, 100} {
		require.NoError(t, cl.LogCommit(id))
	}

	removed, err := cl.Purge(100, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, removed)
	assert.False(t, cl.IsCommitted(1))
	assert.False(t, cl.IsCommitted(5))
	assert.True(t, cl.IsCommitted(100))

	// Appends land after the rewrite and survive a reopen.
	require.NoError(t, cl.LogCommit(101))
	require.NoError(t, cl.Close())

	cl2, err := OpenCommitLog(path)
	require.NoError(t, err)
	defer cl2.Close()
	assert.True(t, cl2.IsCommitted(100))
	assert.True(t, cl2.IsCommitted(101))
	assert.False(t, cl2.IsCommitted(5))
}

// Just before the wrap, the old epoch's head is ancient and goes, while its
// recent tail stays. Reusing id 1 in the new epoch must not inherit the old
// commit mark.
func TestCommitLogPurgeAcrossWrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clog")
	cl, err := OpenCommitLog(path)
	require.NoError(t, err)
	defer cl.Close()

	require.NoError(t, cl.LogCommit(1))
	require.NoError(t, cl.LogCommit(math.MaxUint64-5))

	removed, err := cl.Purge(math.MaxUint64, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.False(t, cl.IsCommitted(1), "old id 1 must not survive into the next pass")
	assert.True(t, cl.IsCommitted(math.MaxUint64-5), "recent tail stays")

	// The first id the new epoch hands out is clean.
	require.NoError(t, cl.LogCommit(1))
	assert.True(t, cl.IsCommitted(1))
}

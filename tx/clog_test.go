package tx

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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

	clog1.LogCommit(10)
	clog1.LogAbort(20)
	clog1.LogCommit(30)
	clog1.Close()

	// Reopen and verify
	clog2, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)
	defer clog2.Close()

	assert.True(t, clog2.IsCommitted(10))
	assert.True(t, clog2.IsAborted(20))
	assert.True(t, clog2.IsCommitted(30))
	assert.False(t, clog2.IsCommitted(999))
}

func TestCommitLogOverwrite(t *testing.T) {
	clogFile := "test_clog_overwrite.data"
	defer os.Remove(clogFile)

	clog, err := OpenCommitLog(clogFile)
	assert.NoError(t, err)
	defer clog.Close()

	// Commit tx=1
	clog.LogCommit(1)
	assert.True(t, clog.IsCommitted(1))

	// Abort tx=1 (overwrite)
	clog.LogAbort(1)
	assert.False(t, clog.IsCommitted(1))
	assert.True(t, clog.IsAborted(1))
}

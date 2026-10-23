package tx

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransaction(t *testing.T) {
	tx := newTransaction(1, RepeatableRead)

	assert.Equal(t, uint64(1), tx.ID())
	assert.Equal(t, StatusActive, tx.Status())

	tx.commit()
	assert.Equal(t, StatusCommitted, tx.Status())

	tx2 := newTransaction(2, ReadCommitted)
	tx2.rollback()
	assert.Equal(t, StatusAborted, tx2.Status())
}

func TestNextCID(t *testing.T) {
	tx := newTransaction(1, RepeatableRead)

	// Each call returns incrementing CID
	assert.Equal(t, uint32(0), tx.NextCID())
	assert.Equal(t, uint32(1), tx.NextCID())
	assert.Equal(t, uint32(2), tx.NextCID())
}

func TestManagerSnapshot(t *testing.T) {
	mgr := NewManager()

	// RepeatableRead: the cutoff is the transaction's own id.
	tx1 := mgr.Begin(RepeatableRead) // id 1
	assert.Equal(t, uint64(1), mgr.Snapshot(tx1).Cutoff)
	assert.Equal(t, uint64(1), mgr.Snapshot(tx1).Cutoff) // same every time

	// ReadCommitted: the cutoff is the next unassigned id. Running a
	// statement does not move it; starting a transaction does.
	reader := mgr.Begin(ReadCommitted) // id 2
	assert.Equal(t, uint64(3), mgr.Snapshot(reader).Cutoff)
	assert.Equal(t, uint64(3), mgr.Snapshot(reader).Cutoff)

	mgr.Begin(RepeatableRead) // id 3
	assert.Equal(t, uint64(4), mgr.Snapshot(reader).Cutoff)
}

func TestReadCommittedSnapshotCoversIssuedIDs(t *testing.T) {
	mgr := NewManager()
	reader := mgr.Begin(ReadCommitted) // id 1

	var last *Transaction
	for i := 0; i < 6; i++ {
		last = mgr.Begin(RepeatableRead) // ids 2..7
	}

	snap := mgr.Snapshot(reader)
	assert.Greater(t, snap.Cutoff, last.ID(),
		"a statement snapshot must cover every id already issued")
}

func TestManager(t *testing.T) {
	mgr := NewManager()

	// Begin two transactions
	tx1 := mgr.Begin(RepeatableRead)
	tx2 := mgr.Begin(ReadCommitted)

	assert.Equal(t, uint64(1), tx1.ID())
	assert.Equal(t, uint64(2), tx2.ID())
	assert.True(t, mgr.IsActive(1))
	assert.True(t, mgr.IsActive(2))

	// Commit tx1
	mgr.Commit(tx1)
	assert.False(t, mgr.IsActive(1))
	assert.True(t, mgr.IsActive(2))

	// Rollback tx2
	mgr.Rollback(tx2)
	assert.False(t, mgr.IsActive(1))
	assert.False(t, mgr.IsActive(2))
}

func TestManagerMultiple(t *testing.T) {
	mgr := NewManager()

	// Begin 5 transactions
	transactions := make([]*Transaction, 5)
	for i := 0; i < 5; i++ {
		transactions[i] = mgr.Begin(RepeatableRead)
	}

	// All should be active
	for i := 0; i < 5; i++ {
		assert.True(t, mgr.IsActive(uint64(i+1)), "tx %d should be active", i+1)
	}

	// Commit even ones, rollback odd ones
	for i, tx := range transactions {
		if i%2 == 0 {
			mgr.Commit(tx)
		} else {
			mgr.Rollback(tx)
		}
	}

	// All should be inactive
	for i := 0; i < 5; i++ {
		assert.False(t, mgr.IsActive(uint64(i+1)), "tx %d should be inactive", i+1)
	}
}

func TestSetNextIDOnlyMovesForward(t *testing.T) {
	mgr := NewManager()
	mgr.Begin(RepeatableRead) // spends id 1, counter now 2
	require.Equal(t, uint64(2), mgr.NextID())

	// Recovery on a crash with nothing to replay returns zero. Taking it
	// literally would rewind the counter onto ids living rows carry.
	mgr.SetNextID(0)
	assert.Equal(t, uint64(2), mgr.NextID())

	mgr.SetNextID(1)
	assert.Equal(t, uint64(2), mgr.NextID())

	// A higher value from recovery moves it forward.
	mgr.SetNextID(50)
	assert.Equal(t, uint64(50), mgr.NextID())

	// And the next Begin hands out the restored value.
	assert.Equal(t, uint64(50), mgr.Begin(RepeatableRead).ID())
}

func TestRestoreAcceptsNewEpoch(t *testing.T) {
	mgr := NewManager()
	mgr.SetNextID(10)

	// A post-wrap checkpoint has a smaller id but a higher epoch.
	mgr.Restore(1, 1)
	assert.Equal(t, uint64(1), mgr.Epoch())
	assert.Equal(t, uint64(1), mgr.NextID())

	// A stale pre-wrap checkpoint must not rewind either field.
	mgr.Restore(math.MaxUint64, 0)
	assert.Equal(t, uint64(1), mgr.Epoch())
	assert.Equal(t, uint64(1), mgr.NextID())

	// Within the same epoch, only a higher id moves the counter.
	mgr.Restore(5, 1)
	assert.Equal(t, uint64(5), mgr.NextID())
	mgr.Restore(3, 1)
	assert.Equal(t, uint64(5), mgr.NextID())

	// The frozen marker is never a counter value.
	mgr.Restore(0, 2)
	assert.Equal(t, uint64(1), mgr.Epoch())
	assert.Equal(t, uint64(5), mgr.NextID())
}

func TestBeginSkipsFrozenID(t *testing.T) {
	mgr := NewManager()
	mgr.SetNextID(math.MaxUint64)

	last := mgr.Begin(RepeatableRead)
	assert.Equal(t, uint64(math.MaxUint64), last.ID())

	first := mgr.Begin(RepeatableRead)
	assert.Equal(t, uint64(1), first.ID(), "the id clock must step over the frozen marker")
}

func TestBeginWrapAdvancesEpoch(t *testing.T) {
	mgr := NewManager()
	mgr.SetNextID(math.MaxUint64)

	last := mgr.Begin(RepeatableRead)
	assert.Equal(t, uint64(math.MaxUint64), last.ID())
	assert.Equal(t, uint64(1), mgr.NextID(), "no id 0 gap to checkpoint")
	assert.Equal(t, uint64(1), mgr.Epoch())

	first := mgr.Begin(RepeatableRead)
	assert.Equal(t, uint64(1), first.ID())
	assert.Equal(t, uint64(2), mgr.NextID())
	assert.Equal(t, uint64(1), mgr.Epoch())
}

func TestBefore(t *testing.T) {
	assert.True(t, Before(1, 2))
	assert.False(t, Before(2, 1))
	assert.False(t, Before(5, 5))
	assert.True(t, Before(math.MaxUint64, 1), "the wrap makes MaxUint64 older than 1")
	assert.False(t, Before(1, math.MaxUint64))
}

func TestAge(t *testing.T) {
	assert.Equal(t, uint64(1), Age(1, 2))
	assert.Equal(t, uint64(0), Age(7, 7))
	assert.Equal(t, uint64(2), Age(math.MaxUint64, 1), "MaxUint64 → 0 → 1")
}

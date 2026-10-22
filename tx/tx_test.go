package tx

import (
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

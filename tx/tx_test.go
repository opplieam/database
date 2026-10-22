package tx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransaction(t *testing.T) {
	tx := NewTransaction(1, RepeatableRead)

	assert.Equal(t, uint64(1), tx.ID())
	assert.Equal(t, TxStatusActive, tx.Status())

	tx.Commit()
	assert.Equal(t, TxStatusCommitted, tx.Status())

	tx2 := NewTransaction(2, ReadCommitted)
	tx2.Rollback()
	assert.Equal(t, TxStatusAborted, tx2.Status())
}

func TestNextCID(t *testing.T) {
	tx := NewTransaction(1, RepeatableRead)

	// Each call returns incrementing CID
	assert.Equal(t, uint32(0), tx.NextCID())
	assert.Equal(t, uint32(1), tx.NextCID())
	assert.Equal(t, uint32(2), tx.NextCID())
}

func TestSnapshotId(t *testing.T) {
	mgr := NewManager()

	// RepeatableRead: always returns tx.ID()
	tx1 := NewTransaction(5, RepeatableRead)
	assert.Equal(t, uint64(5), tx1.SnapshotId(mgr))
	assert.Equal(t, uint64(5), tx1.SnapshotId(mgr)) // same every time

	// ReadCommitted: returns new statement ID each time
	tx2 := NewTransaction(6, ReadCommitted)
	snap1 := tx2.SnapshotId(mgr)
	snap2 := tx2.SnapshotId(mgr)
	assert.NotEqual(t, snap1, snap2, "ReadCommitted should return different snapshot IDs")
	assert.True(t, snap2 > snap1, "snapshot IDs should be increasing")
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

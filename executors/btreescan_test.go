package executors

import (
	"testing"

	"database/btree"
	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestBTreeScan(t *testing.T) {
	tree := btree.NewBTree()

	// Insert records with TIDs
	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}

	// Mock tuple reader that returns a tuple based on TID
	readTuple := func(tid storage.TID) (storage.Tuple, error) {
		return storage.Tuple{int(tid.SlotId) * 10, "Movie", "Genre"}, nil
	}

	// Scan all records
	scan := NewBTreeScan(tree, readTuple, nil)
	result, err := Run(scan)

	assert.NoError(t, err)
	assert.Equal(t, 10, len(result), "should scan 10 records")

	// Verify order
	for i, r := range result {
		expected := (i + 1) * 10
		assert.Equal(t, expected, r[0], "record %d should have key %d", i, expected)
	}
}

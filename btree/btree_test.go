package btree

import (
	"os"
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestBTreeInsertAndSearch(t *testing.T) {
	tree := NewBTree()

	records := []struct {
		key int
		tid storage.TID
	}{
		{30, storage.TID{PageId: 0, SlotId: 0}},
		{10, storage.TID{PageId: 0, SlotId: 1}},
		{20, storage.TID{PageId: 0, SlotId: 2}},
		{40, storage.TID{PageId: 0, SlotId: 3}},
		{50, storage.TID{PageId: 0, SlotId: 4}},
	}

	for _, r := range records {
		tree.Insert(r.key, r.tid)
	}

	// Search existing keys
	for _, r := range records {
		tid, ok := tree.Search(r.key)
		assert.True(t, ok, "Search(%d): should be found", r.key)
		assert.Equal(t, r.tid, tid, "Search(%d): TID mismatch", r.key)
	}

	// Search missing key
	_, ok := tree.Search(99)
	assert.False(t, ok, "Search(99): should not be found")
}

func TestBTreeRangeScan(t *testing.T) {
	tree := NewBTree()

	for i := 1; i <= 15; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}

	keys := tree.RangeScanKeys(25, 55)
	expected := []int{30, 40, 50}
	assert.Equal(t, expected, keys)
}

func TestBTreeDelete(t *testing.T) {
	tree := NewBTree()

	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}

	// Delete some records
	tree.Delete(30)
	tree.Delete(50)
	tree.Delete(70)

	// Deleted keys should not be found
	deletedKeys := []int{30, 50, 70}
	for _, key := range deletedKeys {
		_, ok := tree.Search(key)
		assert.False(t, ok, "Search(%d) after delete: should not be found", key)
	}

	// Remaining keys should still exist
	remainingKeys := []int{10, 20, 40, 60, 80, 90, 100}
	for _, key := range remainingKeys {
		_, ok := tree.Search(key)
		assert.True(t, ok, "Search(%d) after delete: should be found", key)
	}
}

func TestBTreePersistence(t *testing.T) {
	tree := NewBTree()

	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}

	// Save to file
	filename := "test_btree.data"
	defer os.Remove(filename)

	err := tree.Save(filename)
	assert.NoError(t, err, "Save error")

	// Load from file
	loadedTree, err := LoadBTree(filename)
	assert.NoError(t, err, "LoadBTree error")

	// Verify loaded tree
	for i := 1; i <= 10; i++ {
		tid, ok := loadedTree.Search(i * 10)
		assert.True(t, ok, "Search(%d) on loaded tree: not found", i*10)
		assert.Equal(t, storage.TID{PageId: 0, SlotId: uint16(i)}, tid, "Search(%d) on loaded tree: TID mismatch", i*10)
	}
}

func TestBTreeDeleteByTID(t *testing.T) {
	tree := NewBTree()

	// Insert entries
	tid1 := storage.TID{PageId: 0, SlotId: 1}
	tid2 := storage.TID{PageId: 0, SlotId: 2}
	tid3 := storage.TID{PageId: 1, SlotId: 1}

	tree.Insert(10, tid1)
	tree.Insert(20, tid2)
	tree.Insert(30, tid3)

	// Delete by TID
	removed := tree.DeleteByTID(tid2)
	assert.True(t, removed, "should remove entry with matching TID")

	// Verify entry removed
	_, ok := tree.Search(20)
	assert.False(t, ok, "Search(20) after delete: should not be found")

	// Verify other entries still exist
	_, ok = tree.Search(10)
	assert.True(t, ok, "Search(10) after delete: should be found")
	_, ok = tree.Search(30)
	assert.True(t, ok, "Search(30) after delete: should be found")

	// Try to delete non-existent TID
	removed = tree.DeleteByTID(storage.TID{PageId: 99, SlotId: 99})
	assert.False(t, removed, "should return false for non-existent TID")
}

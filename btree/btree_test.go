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

// collectLeafKeys walks the leaf chain from the leftmost leaf and returns the
// keys of each leaf, in chain order.
//
// Example: leaves [10,20] -> [30,40] -> nil
//
//	collectLeafKeys(t, tree) == [][]int{{10, 20}, {30, 40}}
func collectLeafKeys(t *testing.T, tree *BTree) [][]int {
	t.Helper()

	var leaves [][]int
	seen := make(map[*BTreeNode]bool)
	for leaf := tree.findLeftmostLeaf(); leaf != nil; leaf = leaf.Next {
		if seen[leaf] {
			t.Fatalf("leaf chain loops back to %v", leaf.Keys)
		}
		seen[leaf] = true
		leaves = append(leaves, append([]int(nil), leaf.Keys...))
	}
	return leaves
}

// flattenLeafKeys concatenates per-leaf key slices into one key list.
//
// Example: [][]int{{10, 20}, {30}} -> []int{10, 20, 30}
func flattenLeafKeys(leaves [][]int) []int {
	var keys []int
	for _, leaf := range leaves {
		keys = append(keys, leaf...)
	}
	return keys
}

// TestBTreeSaveLoadPreservesLeafChain covers the part of the file format that
// TestBTreePersistence cannot see: the Next pointer between leaves.
//
// Search only walks down from the root, so it still passes when every Next
// pointer is nil after a load. RangeScanKeys follows the chain, so the range
// checks below fail if LoadBTree did not rebuild it.
//
// Example: 12 keys with order 4 split into 6 leaves of 2 keys
//
//	[10,20] -> [30,40] -> [50,60] -> [70,80] -> [90,100] -> [110,120]
func TestBTreeSaveLoadPreservesLeafChain(t *testing.T) {
	tree := NewBTree()
	for i := 1; i <= 12; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}

	before := collectLeafKeys(t, tree)
	if len(before) < 2 {
		t.Fatalf("expected several leaves, got %d: the chain is not exercised", len(before))
	}

	filename := "test_btree_chain.data"
	defer os.Remove(filename)
	assert.NoError(t, tree.Save(filename), "Save error")

	loaded, err := LoadBTree(filename)
	assert.NoError(t, err, "LoadBTree error")

	// Same partition, not just the same keys: a lost Next shows up here.
	assert.Equal(t, before, collectLeafKeys(t, loaded), "leaf partition changed by save/load")

	// A range that starts mid-chain has to follow Next to finish.
	assert.Equal(t, []int{40, 50, 60, 70, 80, 90}, loaded.RangeScanKeys(35, 95), "range scan over the loaded chain")
	// The last leaf's Next must be nil, or the walk runs past the end.
	assert.Equal(t, flattenLeafKeys(before), loaded.RangeScanKeys(0, 1000), "full range over the loaded chain")
}

// TestBTreeDeleteMergesIntoRightSibling drives the first leaf below the minimum
// fill when it has no left neighbour and its right neighbour cannot spare a key.
// handleUnderflow then falls through to mergeWithRight.
//
// Example: order 4, minimum 2 keys per leaf
//
//	before: [10,20] -> [30,40] -> [50,60] -> [70,80] -> [90,100] -> [110,120]
//	delete 10
//	after:  [20,30,40] -> [50,60] -> [70,80] -> [90,100] -> [110,120]
func TestBTreeDeleteMergesIntoRightSibling(t *testing.T) {
	tree := NewBTree()
	for i := 1; i <= 12; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}
	before := collectLeafKeys(t, tree)

	tree.Delete(10)

	after := collectLeafKeys(t, tree)
	assert.Len(t, after, len(before)-1, "merge removes exactly one leaf from the chain")

	expected := []int{20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120}
	assert.Equal(t, expected, flattenLeafKeys(after), "keys after merge")
	assert.Equal(t, expected, tree.RangeScanKeys(0, 1000), "range scan after merge")

	_, ok := tree.Search(10)
	assert.False(t, ok, "Search(10) after delete: should not be found")
	for _, key := range expected {
		_, ok := tree.Search(key)
		assert.True(t, ok, "Search(%d) after merge: should be found", key)
	}
}

// TestBTreeDeleteBorrowsFromRightSibling drives the first leaf below the minimum
// fill while its right neighbour holds a spare key, so handleUnderflow calls
// borrowFromRight instead of merging. Nothing else in the suite reaches that
// branch today.
//
// Example: order 4, minimum 2 keys per leaf
//
//	before: [10,20] -> [30,40,50]   parent key 30
//	delete 10
//	after:  [20,30] -> [40,50]      parent key 30 -> 40
func TestBTreeDeleteBorrowsFromRightSibling(t *testing.T) {
	tree := NewBTree()
	for _, key := range []int{10, 20, 30, 40, 50} {
		tree.Insert(key, storage.TID{PageId: 0, SlotId: uint16(key / 10)})
	}

	before := collectLeafKeys(t, tree)
	tree.Delete(10)
	after := collectLeafKeys(t, tree)

	assert.Len(t, after, len(before), "borrowing keeps the leaf count")
	assert.Equal(t, [][]int{{20, 30}, {40, 50}}, after, "keys after borrow")
	assert.Equal(t, []int{20, 30, 40, 50}, tree.RangeScanKeys(0, 1000), "range scan after borrow")

	// The TID has to travel with its key, not stay behind in the sibling.
	tid, ok := tree.Search(30)
	assert.True(t, ok, "Search(30) after borrow: should be found")
	assert.Equal(t, storage.TID{PageId: 0, SlotId: 3}, tid, "Search(30) after borrow: TID mismatch")
}

// TestBTreeDeleteMergesInternalIntoLeftSibling drives the leftward merge of two
// internal nodes, the one path the other delete tests never reach.
//
// Example: order 4, minimum 2 keys per leaf and 2 children per internal node
//
//	before: root[50,90] -> A[30] (L1,R1), B[70] (R2,R3), C[110] (R4,R5)
//	delete 110: R5=[120] underflows, merges into R4 -> R4=[90,100,120], C=[]
//	            C underflows, merges into B -> B=[70,90], root=[50]
//	after:  [10,20] -> [30,40] -> [50,60] -> [70,80] -> [90,100,120]
func TestBTreeDeleteMergesInternalIntoLeftSibling(t *testing.T) {
	tree := NewBTree()
	for i := 1; i <= 12; i++ {
		tree.Insert(i*10, storage.TID{PageId: 0, SlotId: uint16(i)})
	}
	before := collectLeafKeys(t, tree)

	tree.Delete(110)

	after := collectLeafKeys(t, tree)
	assert.Len(t, after, len(before)-1, "one leaf disappears per merge")

	expected := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 120}
	assert.Equal(t, expected, flattenLeafKeys(after), "keys after internal merge")
	assert.Equal(t, expected, tree.RangeScanKeys(0, 1000), "range scan after internal merge")

	_, ok := tree.Search(110)
	assert.False(t, ok, "Search(110) after delete: should not be found")
	_, ok = tree.Search(120)
	assert.True(t, ok, "Search(120) after merge: should be found")
}

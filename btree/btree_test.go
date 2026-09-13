package btree

import (
	"fmt"
	"os"
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestBTreeInsertAndSearch(t *testing.T) {
	tree := NewBTree()

	records := []struct {
		key   int
		value storage.Tuple
	}{
		{30, storage.Tuple{30, "Movie 30", "Action"}},
		{10, storage.Tuple{10, "Movie 10", "Comedy"}},
		{20, storage.Tuple{20, "Movie 20", "Drama"}},
		{40, storage.Tuple{40, "Movie 40", "Horror"}},
		{50, storage.Tuple{50, "Movie 50", "SciFi"}},
	}

	for _, r := range records {
		tree.Insert(r.key, r.value)
	}

	// Search existing keys
	for _, r := range records {
		val, ok := tree.Search(r.key)
		assert.True(t, ok, "Search(%d): should be found", r.key)
		assert.Equal(t, r.value[0], val[0], "Search(%d): value mismatch", r.key)
	}

	// Search missing key
	_, ok := tree.Search(99)
	assert.False(t, ok, "Search(99): should not be found")
}

func TestBTreeRangeScan(t *testing.T) {
	tree := NewBTree()

	for i := 1; i <= 15; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
	}

	keys := tree.RangeScanKeys(25, 55)
	expected := []int{30, 40, 50}
	assert.Equal(t, expected, keys)
}

func TestBTreeDelete(t *testing.T) {
	tree := NewBTree()

	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
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
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
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
		val, ok := loadedTree.Search(i * 10)
		assert.True(t, ok, "Search(%d) on loaded tree: not found", i*10)
		assert.Equal(t, fmt.Sprintf("%d", i*10), val[0], "Search(%d) on loaded tree: key mismatch", i*10)
	}
}

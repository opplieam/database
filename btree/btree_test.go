package btree

import (
	"fmt"
	"os"
	"testing"

	"database/storage"
)

func TestBTreeInsertAndSearch(t *testing.T) {
	tree := NewBTree()

	// Insert records
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

	// Test search
	for _, r := range records {
		val, ok := tree.Search(r.key)
		if !ok {
			t.Errorf("Search(%d): not found", r.key)
			continue
		}
		if val[0] != r.value[0] {
			t.Errorf("Search(%d): expected %v, got %v", r.key, r.value, val)
		}
	}

	// Test missing key
	if _, ok := tree.Search(99); ok {
		t.Error("Search(99): should not be found")
	}
}

func TestBTreeRangeScan(t *testing.T) {
	tree := NewBTree()

	// Insert 15 records
	for i := 1; i <= 15; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
	}

	// Range scan
	keys := tree.RangeScanKeys(25, 55)
	expected := []int{30, 40, 50}
	if len(keys) != len(expected) {
		t.Fatalf("RangeScan(25, 55): expected %v, got %v", expected, keys)
	}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("RangeScan(25, 55)[%d]: expected %d, got %d", i, expected[i], k)
		}
	}
}

func TestBTreeDelete(t *testing.T) {
	tree := NewBTree()

	// Insert records
	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
	}

	// Delete some records
	tree.Delete(30)
	tree.Delete(50)
	tree.Delete(70)

	// Verify deleted records are gone
	if _, ok := tree.Search(30); ok {
		t.Error("Search(30) after delete: should not be found")
	}
	if _, ok := tree.Search(50); ok {
		t.Error("Search(50) after delete: should not be found")
	}
	if _, ok := tree.Search(70); ok {
		t.Error("Search(70) after delete: should not be found")
	}

	// Verify remaining records still exist
	for _, key := range []int{10, 20, 40, 60, 80, 90, 100} {
		if _, ok := tree.Search(key); !ok {
			t.Errorf("Search(%d) after delete: should be found", key)
		}
	}
}

func TestBTreePersistence(t *testing.T) {
	tree := NewBTree()

	// Insert records
	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
	}

	// Save to file
	filename := "test_btree.data"
	defer os.Remove(filename)

	if err := tree.Save(filename); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Load from file
	loadedTree, err := LoadBTree(filename)
	if err != nil {
		t.Fatalf("LoadBTree error: %v", err)
	}

	// Verify loaded tree
	// Note: decoded tuples have strings (serialization converts types to strings)
	for i := 1; i <= 10; i++ {
		val, ok := loadedTree.Search(i * 10)
		if !ok {
			t.Errorf("Search(%d) on loaded tree: not found", i*10)
			continue
		}
		// val[0] is string "10", val[1] is "Movie", val[2] is "Genre"
		if val[0] != fmt.Sprintf("%d", i*10) {
			t.Errorf("Search(%d) on loaded tree: expected key %d, got %v", i*10, i*10, val[0])
		}
	}
}

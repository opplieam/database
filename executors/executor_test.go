package executors

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"database/btree"
	"database/storage"
)

func TestQueryExecutors(t *testing.T) {
	birds := []storage.Tuple{
		{"amerob", "American Robin", 0.077, true},
		{"baleag", "Bald Eagle", 4.74, true},
		{"eursta", "European Starling", 0.082, true},
		{"barswa", "Barn Swallow", 0.019, true},
		{"ostric1", "Ostrich", 104.0, false},
		{"emppen1", "Emperor Penguin", 23.0, false},
		{"rufhum", "Rufous Hummingbird", 0.0034, true},
		{"comrav", "Common Raven", 1.2, true},
		{"wanalb", "Wandering Albatross", 8.5, false},
		{"norcar", "Northern Cardinal", 0.045, true},
	}

	// Test 1: Non-US bird IDs
	q1 := NewProjection(
		NewSelection(
			NewMemoryScan(birds),
			func(t storage.Tuple) bool { return !t[3].(bool) },
		),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[0], t[1]} },
	)
	result1, err := Run(q1)
	if err != nil {
		t.Fatalf("q1 error: %v", err)
	}
	if len(result1) != 3 {
		t.Errorf("expected 3 non-US birds, got %d", len(result1))
	}

	// Test 2: Top 3 heaviest birds
	q2 := NewProjection(
		NewLimit(
			NewSort(
				NewMemoryScan(birds),
				func(t storage.Tuple) any { return t[2] },
				true,
			),
			3,
		),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[0], t[2]} },
	)
	result2, err := Run(q2)
	if err != nil {
		t.Fatalf("q2 error: %v", err)
	}
	if len(result2) != 3 {
		t.Errorf("expected 3 heaviest birds, got %d", len(result2))
	}
	// Verify order: Ostrich (104) > Emperor Penguin (23) > Wandering Albatross (8.5)
	if result2[0][0] != "ostric1" {
		t.Errorf("expected ostric1 first, got %v", result2[0][0])
	}
}

func TestHeapFileScan(t *testing.T) {
	// Create test file
	movies := []storage.MovieRecord{
		{1, "Toy Story", "Adventure"},
		{2, "Jumanji", "Comedy"},
		{3, "Grumpier Old Men", "Comedy"},
		{4, "Heat", "Action"},
	}

	filename := "test_heap_scan.data"
	defer os.Remove(filename)

	if err := storage.WriteMoviesPages(filename, movies); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// Test 1: Read all records
	scan, err := NewHeapFileScan(filename)
	if err != nil {
		t.Fatalf("heap scan error: %v", err)
	}
	defer scan.Close()

	q1 := NewProjection(
		NewLimit(scan, 5),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[1]} },
	)
	result1, err := Run(q1)
	if err != nil {
		t.Fatalf("q1 error: %v", err)
	}
	if len(result1) != 4 {
		t.Errorf("expected 4 movies, got %d", len(result1))
	}

	// Test 2: Filter by genre
	scan2, err := NewHeapFileScan(filename)
	if err != nil {
		t.Fatalf("heap scan error: %v", err)
	}
	defer scan2.Close()

	q2 := NewSelection(
		scan2,
		func(t storage.Tuple) bool {
			genres := t[2].(string)
			return strings.Contains(genres, "Comedy")
		},
	)
	result2, err := Run(q2)
	if err != nil {
		t.Fatalf("q2 error: %v", err)
	}
	if len(result2) != 2 {
		t.Errorf("expected 2 Comedy movies, got %d", len(result2))
	}
}

func TestInsert(t *testing.T) {
	filename := "test_insert.data"
	defer os.Remove(filename)

	// Test 1: Insert into new file
	insert1 := NewInsert(filename, storage.MovieRecord{1, "First Movie", "Action"}, 0)
	_, err := insert1.Next()
	if err != nil {
		t.Fatalf("insert1 error: %v", err)
	}

	movies1, err := storage.ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(movies1) != 1 {
		t.Errorf("expected 1 record, got %d", len(movies1))
	}

	// Test 2: Insert into existing file
	insert2 := NewInsert(filename, storage.MovieRecord{2, "Second Movie", "Comedy"}, 0)
	_, err = insert2.Next()
	if err != nil {
		t.Fatalf("insert2 error: %v", err)
	}

	movies2, err := storage.ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(movies2) != 2 {
		t.Errorf("expected 2 records, got %d", len(movies2))
	}

	// Test 3: Insert with NULL bitmap
	insert3 := NewInsert(filename, storage.MovieRecord{3, "", "Drama"}, 2) // NULL title
	_, err = insert3.Next()
	if err != nil {
		t.Fatalf("insert3 error: %v", err)
	}

	movies3, err := storage.ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(movies3) != 3 {
		t.Errorf("expected 3 records, got %d", len(movies3))
	}
	if movies3[2].Title != "" {
		t.Errorf("expected empty title for NULL, got %q", movies3[2].Title)
	}
}

func TestInsertOverflow(t *testing.T) {
	filename := "test_insert_overflow.data"
	defer os.Remove(filename)

	// Insert 100 records to test page overflow
	for i := uint32(1); i <= 100; i++ {
		insert := NewInsert(filename, storage.MovieRecord{i, fmt.Sprintf("Movie %d", i), "Action"}, 0)
		if _, err := insert.Next(); err != nil {
			t.Fatalf("insert %d error: %v", i, err)
		}
	}

	movies, err := storage.ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(movies) != 100 {
		t.Errorf("expected 100 records, got %d", len(movies))
	}
}

func TestBTreeScan(t *testing.T) {
	tree := btree.NewBTree()

	// Insert records
	for i := 1; i <= 10; i++ {
		tree.Insert(i*10, storage.Tuple{i * 10, "Movie", "Genre"})
	}

	// Scan all records
	scan := NewBTreeScan(tree)
	result, err := Run(scan)
	if err != nil {
		t.Fatalf("BTreeScan error: %v", err)
	}

	if len(result) != 10 {
		t.Errorf("expected 10 records, got %d", len(result))
	}

	// Verify order
	for i, r := range result {
		expected := (i + 1) * 10
		if r[0] != expected {
			t.Errorf("record %d: expected key %d, got %v", i, expected, r[0])
		}
	}
}

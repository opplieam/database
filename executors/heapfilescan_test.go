package executors

import (
	"os"
	"strings"
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

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

	err := storage.WriteMoviesPages(filename, movies)
	assert.NoError(t, err)

	// Test 1: Read all records
	scan, err := NewHeapFileScan(filename)
	assert.NoError(t, err)
	defer scan.Close()

	q1 := NewProjection(
		NewLimit(scan, 5),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[1]} },
	)
	result1, err := Run(q1)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(result1), "should read all 4 movies")

	// Test 2: Filter by genre
	scan2, err := NewHeapFileScan(filename)
	assert.NoError(t, err)
	defer scan2.Close()

	q2 := NewSelection(
		scan2,
		func(t storage.Tuple) bool {
			genres := t[2].(string)
			return strings.Contains(genres, "Comedy")
		},
	)
	result2, err := Run(q2)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(result2), "should find 2 Comedy movies")
}

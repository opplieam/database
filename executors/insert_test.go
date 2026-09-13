package executors

import (
	"fmt"
	"os"
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestInsert(t *testing.T) {
	filename := "test_insert_executor.data"
	defer os.Remove(filename)

	// Test 1: Insert into new file
	insert1 := NewInsert(filename, storage.MovieRecord{1, "First Movie", "Action"}, 0)
	_, err := insert1.Next()
	assert.NoError(t, err)

	movies1, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(movies1), "should have 1 record")

	// Test 2: Insert into existing file
	insert2 := NewInsert(filename, storage.MovieRecord{2, "Second Movie", "Comedy"}, 0)
	_, err = insert2.Next()
	assert.NoError(t, err)

	movies2, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(movies2), "should have 2 records")

	// Test 3: Insert with NULL bitmap
	insert3 := NewInsert(filename, storage.MovieRecord{3, "", "Drama"}, 2) // NULL title
	_, err = insert3.Next()
	assert.NoError(t, err)

	movies3, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(movies3), "should have 3 records")
	assert.Equal(t, "", movies3[2].Title, "NULL title should be empty")
}

func TestInsertOverflow(t *testing.T) {
	filename := "test_insert_overflow_executor.data"
	defer os.Remove(filename)

	// Insert 100 records to test page overflow
	for i := uint32(1); i <= 100; i++ {
		insert := NewInsert(filename, storage.MovieRecord{i, fmt.Sprintf("Movie %d", i), "Action"}, 0)
		_, err := insert.Next()
		assert.NoError(t, err, "insert %d should succeed", i)
	}

	movies, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 100, len(movies), "should have 100 records")
}

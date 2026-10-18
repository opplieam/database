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
	defer os.Remove(filename + ".fsm")

	// Test 1: Insert into new file
	insert1 := NewInsert(filename, storage.MovieRecord{MovieId: 1, Title: "First Movie", Genres: "Action"}, 0, nil)
	_, err := insert1.Next()
	assert.NoError(t, err)

	movies1, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(movies1), "should have 1 record")

	// Test 2: Insert into existing file
	insert2 := NewInsert(filename, storage.MovieRecord{MovieId: 2, Title: "Second Movie", Genres: "Comedy"}, 0, nil)
	_, err = insert2.Next()
	assert.NoError(t, err)

	movies2, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(movies2), "should have 2 records")

	// Test 3: Insert with NULL bitmap
	insert3 := NewInsert(filename, storage.MovieRecord{MovieId: 3, Title: "", Genres: "Drama"}, 2, nil) // NULL title
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
	defer os.Remove(filename + ".fsm")

	// Insert 100 records to test page overflow
	for i := uint32(1); i <= 100; i++ {
		insert := NewInsert(filename, storage.MovieRecord{MovieId: i, Title: fmt.Sprintf("Movie %d", i), Genres: "Action"}, 0, nil)
		_, err := insert.Next()
		assert.NoError(t, err, "insert %d should succeed", i)
	}

	movies, err := storage.ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 100, len(movies), "should have 100 records")
}

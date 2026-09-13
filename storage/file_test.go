package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileRoundtrip(t *testing.T) {
	original := []MovieRecord{
		{1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{3, "Grumpier Old Men (1995)", "Comedy|Romance"},
	}

	filename := "test_roundtrip.data"
	defer os.Remove(filename)

	err := WriteMovies(filename, original)
	assert.NoError(t, err)

	readBack, err := ReadMovies(filename)
	assert.NoError(t, err)
	assert.Equal(t, original, readBack)
}

func TestSlottedPages(t *testing.T) {
	original := []MovieRecord{
		{1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{3, "Grumpier Old Men (1995)", "Comedy|Romance"},
	}

	filename := "test_pages.data"
	defer os.Remove(filename)

	err := WriteMoviesPages(filename, original)
	assert.NoError(t, err)

	readBack, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, original, readBack)
}

func TestInsertRecord(t *testing.T) {
	filename := "test_insert_record.data"
	defer os.Remove(filename)

	// Insert into new file
	record1 := MovieRecord{1, "First Movie", "Action"}
	err := InsertRecord(filename, record1, 0)
	assert.NoError(t, err)

	movies1, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(movies1))
	assert.Equal(t, record1, movies1[0])

	// Insert into existing file
	record2 := MovieRecord{2, "Second Movie", "Comedy"}
	err = InsertRecord(filename, record2, 0)
	assert.NoError(t, err)

	movies2, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(movies2))
	assert.Equal(t, record1, movies2[0])
	assert.Equal(t, record2, movies2[1])

	// Insert with NULL bitmap
	record3 := MovieRecord{3, "", "Drama"}
	err = InsertRecord(filename, record3, 2) // NULL title
	assert.NoError(t, err)

	movies3, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(movies3))
	assert.Equal(t, "", movies3[2].Title, "NULL title should be empty")
}

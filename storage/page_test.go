package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPage(t *testing.T) {
	page := NewPage(42)

	assert.Equal(t, uint32(42), page.Header.PageId)
	assert.Equal(t, uint16(0), page.Header.RecordCount)
	assert.Equal(t, uint16(8), page.Header.FreeOffset)
	assert.Empty(t, page.LinePointers)
	assert.Empty(t, page.NullBitmaps)
	assert.Empty(t, page.Records)
}

func TestAddRecord(t *testing.T) {
	page := NewPage(0)

	// Add first record
	record1 := []byte{1, 2, 3, 4, 5}
	ok := page.AddRecord(record1, 0)
	assert.True(t, ok)
	assert.Equal(t, uint16(1), page.Header.RecordCount)

	// Add second record
	record2 := []byte{6, 7, 8}
	ok = page.AddRecord(record2, 2) // nullBitmap=2 (title NULL)
	assert.True(t, ok)
	assert.Equal(t, uint16(2), page.Header.RecordCount)
	assert.Equal(t, uint8(2), page.NullBitmaps[1])
}

func TestIsNull(t *testing.T) {
	tests := []struct {
		name     string
		bitmap   uint8
		column   int
		expected bool
	}{
		{"no nulls", 0b000, 0, false},
		{"movieId null", 0b001, 0, true},
		{"title null", 0b010, 1, true},
		{"genres null", 0b100, 2, true},
		{"multiple nulls", 0b111, 1, true},
		{"column 0 not null", 0b010, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNull(tt.bitmap, tt.column)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPageHasSpace(t *testing.T) {
	page := NewPage(0)

	// Empty page should have space
	assert.True(t, page.HasSpace(100))

	// Add a record
	page.AddRecord(make([]byte, 100), 0)
	assert.True(t, page.HasSpace(100))

	// Empty page with small record
	page2 := NewPage(1)
	assert.True(t, page2.HasSpace(10))
}

func TestPageEncodeDecode(t *testing.T) {
	page := NewPage(7)

	// Add some records
	page.AddRecord([]byte{1, 2, 3}, 0)
	page.AddRecord([]byte{4, 5, 6}, 1)

	// Encode
	encoded := page.Encode()
	assert.Equal(t, PageSize, len(encoded))

	// Decode
	decoded, err := DecodePage(encoded)
	assert.NoError(t, err)

	assert.Equal(t, uint32(7), decoded.Header.PageId)
	assert.Equal(t, uint16(2), decoded.Header.RecordCount)
	assert.Equal(t, uint8(0), decoded.NullBitmaps[0])
	assert.Equal(t, uint8(1), decoded.NullBitmaps[1])
}

func TestPageGetRecord(t *testing.T) {
	page := NewPage(0)

	record1 := []byte{1, 2, 3}
	record2 := []byte{4, 5, 6, 7}

	page.AddRecord(record1, 0)
	page.AddRecord(record2, 0)

	// Get records
	got1, err := page.GetRecord(0)
	assert.NoError(t, err)
	assert.Equal(t, record1, got1)

	got2, err := page.GetRecord(1)
	assert.NoError(t, err)
	assert.Equal(t, record2, got2)

	// Out of bounds
	_, err = page.GetRecord(2)
	assert.Error(t, err)
}

func TestNullBitmaps(t *testing.T) {
	movies := []MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Adventure"},
		{MovieId: 2, Title: "", Genres: "Comedy"},           // NULL title
		{MovieId: 3, Title: "Jumanji", Genres: ""},          // NULL genres
		{MovieId: 0, Title: "No ID Movie", Genres: "Drama"}, // NULL movieId
	}

	filename := "test_null_pages.data"
	defer os.Remove(filename)

	if err := WriteMoviesPages(filename, movies); err != nil {
		t.Fatalf("write error: %v", err)
	}

	readBack, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, len(movies), len(readBack))

	for i, m := range movies {
		assert.Equal(t, m.MovieId, readBack[i].MovieId, "record %d MovieId", i)
		assert.Equal(t, m.Title, readBack[i].Title, "record %d Title", i)
		assert.Equal(t, m.Genres, readBack[i].Genres, "record %d Genres", i)
	}
}

func TestSlottedPagesManyRecords(t *testing.T) {
	// Test with enough records to span multiple pages
	original := make([]MovieRecord, 150)
	for i := uint32(0); i < 150; i++ {
		original[i] = MovieRecord{
			MovieId: i + 1,
			Title:   fmt.Sprintf("Movie %d", i+1),
			Genres:  "Action",
		}
	}

	filename := "test_many_pages.data"
	defer os.Remove(filename)

	if err := WriteMoviesPages(filename, original); err != nil {
		t.Fatalf("write error: %v", err)
	}

	readBack, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, len(original), len(readBack))

	for i := range original {
		assert.Equal(t, original[i], readBack[i], "record %d", i)
	}

	pageCount := len(readBack)/50 + 1
	fmt.Printf("  150 records across %d pages\n", pageCount)
}

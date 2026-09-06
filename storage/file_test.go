package storage

import (
	"fmt"
	"os"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	type movie struct {
		id     uint32
		title  string
		genres string
	}
	movies := []movie{
		{1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{3, "Grumpier Old Men (1995)", "Comedy|Romance"},
	}

	for _, m := range movies {
		encoded := EncodeRecord(m.id, m.title, m.genres)
		decodedId, decodedTitle, decodedGenres, err := DecodeRecord(encoded)
		if err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if decodedId != m.id || decodedTitle != m.title || decodedGenres != m.genres {
			t.Errorf("original=%+v, got=(%d, %q, %q)", m, decodedId, decodedTitle, decodedGenres)
		}
	}
}

func TestFileRoundtrip(t *testing.T) {
	original := []MovieRecord{
		{1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{3, "Grumpier Old Men (1995)", "Comedy|Romance"},
	}

	filename := "test_roundtrip.data"
	defer os.Remove(filename)

	if err := WriteMovies(filename, original); err != nil {
		t.Fatalf("write error: %v", err)
	}

	readBack, err := ReadMovies(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if len(original) != len(readBack) {
		t.Fatalf("count mismatch: original=%d, read=%d", len(original), len(readBack))
	}
	for i := range original {
		if original[i] != readBack[i] {
			t.Errorf("record %d: original=%+v, read=%+v", i, original[i], readBack[i])
		}
	}
}

func TestSlottedPages(t *testing.T) {
	original := []MovieRecord{
		{1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{3, "Grumpier Old Men (1995)", "Comedy|Romance"},
	}

	filename := "test_pages.data"
	defer os.Remove(filename)

	if err := WriteMoviesPages(filename, original); err != nil {
		t.Fatalf("write error: %v", err)
	}

	readBack, err := ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if len(original) != len(readBack) {
		t.Fatalf("count mismatch: original=%d, read=%d", len(original), len(readBack))
	}
	for i := range original {
		if original[i] != readBack[i] {
			t.Errorf("record %d: original=%+v, read=%+v", i, original[i], readBack[i])
		}
	}

	pageCount := len(readBack)/50 + 1
	fmt.Printf("  used %d pages (50 records per page)\n", pageCount)
}

func TestNullBitmaps(t *testing.T) {
	movies := []MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Adventure"},
		{MovieId: 2, Title: "", Genres: "Comedy"},           // NULL title
		{MovieId: 3, Title: "Jumanji", Genres: ""},          // NULL genres
		{MovieId: 0, Title: "No ID Movie", Genres: "Drama"}, // NULL movieId
	}

	filename := "test_null.data"
	defer os.Remove(filename)

	if err := WriteMoviesPages(filename, movies); err != nil {
		t.Fatalf("write error: %v", err)
	}

	readBack, err := ReadMoviesPages(filename)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	for i, m := range movies {
		if readBack[i].MovieId != m.MovieId ||
			readBack[i].Title != m.Title ||
			readBack[i].Genres != m.Genres {
			t.Errorf("record %d: original=%+v, read=%+v", i, m, readBack[i])
		}
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
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if len(original) != len(readBack) {
		t.Fatalf("count mismatch: original=%d, read=%d", len(original), len(readBack))
	}
	for i := range original {
		if original[i] != readBack[i] {
			t.Errorf("record %d: original=%+v, read=%+v", i, original[i], readBack[i])
		}
	}

	fmt.Printf("  150 records across %d pages\n", len(readBack)/50+1)
}

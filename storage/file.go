package storage

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
)

// MovieRecord represents a movie with its ID, title, and genres.
type MovieRecord struct {
	MovieId uint32
	Title   string
	Genres  string
}

// WriteMovies writes a sequential file of movie records.
// Format: [4 bytes count] [record 1] [record 2] ...
func WriteMovies(path string, movies []MovieRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// write count
	count := make([]byte, 4)
	binary.LittleEndian.PutUint32(count, uint32(len(movies)))
	if _, err := f.Write(count); err != nil {
		return err
	}

	// write each record
	for _, m := range movies {
		encoded := EncodeRecord(m.MovieId, m.Title, m.Genres)
		if _, err := f.Write(encoded); err != nil {
			return err
		}
	}

	return nil
}

// ReadMovies reads a sequential file of movie records.
func ReadMovies(path string) ([]MovieRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// read count
	var count uint32
	if err := binary.Read(f, binary.LittleEndian, &count); err != nil {
		return nil, err
	}

	movies := make([]MovieRecord, count)
	for i := uint32(0); i < count; i++ {
		// read movieId (4 bytes)
		var movieId uint32
		if err := binary.Read(f, binary.LittleEndian, &movieId); err != nil {
			return nil, err
		}

		// read title length (1 byte)
		var titleLen uint8
		if err := binary.Read(f, binary.LittleEndian, &titleLen); err != nil {
			return nil, err
		}

		// read title
		titleBytes := make([]byte, titleLen)
		if _, err := io.ReadFull(f, titleBytes); err != nil {
			return nil, err
		}

		// read genres length (1 byte)
		var genresLen uint8
		if err := binary.Read(f, binary.LittleEndian, &genresLen); err != nil {
			return nil, err
		}

		// read genres
		genresBytes := make([]byte, genresLen)
		if _, err := io.ReadFull(f, genresBytes); err != nil {
			return nil, err
		}

		movies[i] = MovieRecord{
			MovieId: movieId,
			Title:   string(titleBytes),
			Genres:  string(genresBytes),
		}
	}

	return movies, nil
}

// WriteMoviesPages writes movies using slotted pages with null bitmaps.
func WriteMoviesPages(path string, movies []MovieRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// write total count
	count := make([]byte, 4)
	binary.LittleEndian.PutUint32(count, uint32(len(movies)))
	if _, err := f.Write(count); err != nil {
		return err
	}

	var pageId uint32
	var page *Page

	for _, m := range movies {
		encoded := EncodeRecord(m.MovieId, m.Title, m.Genres)

		// null bitmap: all zeros (no NULLs) unless explicitly set
		// For CSV data, we don't have NULLs, so use 0
		var nullBitmap uint8

		if page == nil {
			page = NewPage(pageId)
		}

		if !page.AddRecord(encoded, nullBitmap) {
			// page is full, write it and start new one
			if _, err := f.Write(page.Encode()); err != nil {
				return err
			}
			pageId++
			page = NewPage(pageId)

			// try adding again
			if !page.AddRecord(encoded, nullBitmap) {
				return errors.New("record too large for page")
			}
		}
	}

	// write last page
	if page != nil && page.Header.RecordCount > 0 {
		if _, err := f.Write(page.Encode()); err != nil {
			return err
		}
	}

	return nil
}

// ReadMoviesPages reads movies from slotted pages.
func ReadMoviesPages(path string) ([]MovieRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// read total count
	var totalCount uint32
	if err := binary.Read(f, binary.LittleEndian, &totalCount); err != nil {
		return nil, err
	}

	movies := make([]MovieRecord, 0, totalCount)

	for {
		pageData := make([]byte, PageSize)
		_, err := io.ReadFull(f, pageData)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		page, err := DecodePage(pageData)
		if err != nil {
			return nil, err
		}

		for i := 0; i < int(page.Header.RecordCount); i++ {
			recordBytes, err := page.GetRecord(i)
			if err != nil {
				return nil, err
			}

			nullBitmap, err := page.GetNullBitmap(i)
			if err != nil {
				return nil, err
			}

			id, title, genres, err := DecodeRecord(recordBytes)
			if err != nil {
				return nil, err
			}

			// handle nulls based on bitmap
			if IsNull(nullBitmap, 0) {
				id = 0
			}
			if IsNull(nullBitmap, 1) {
				title = ""
			}
			if IsNull(nullBitmap, 2) {
				genres = ""
			}

			movies = append(movies, MovieRecord{
				MovieId: id,
				Title:   title,
				Genres:  genres,
			})
		}
	}

	return movies, nil
}

// InsertRecord inserts one record into a binary slotted-page file.
//
// Steps:
// 1. Open file, read all existing pages into memory
// 2. Find first page with space for the new record
// 3. Add record to that page
// 4. If no page has space, create a new page and add there
// 5. Write all pages back to file
//
// Example: file has 2 pages, page 0 is full (50 records), page 1 has 48 records
//
//   Before:
//     Page 0: 50 records (full)
//     Page 1: 48 records (has space)
//
//   Insert {999, "New Movie", "Action"}:
//     1. Read pages: [page0, page1]
//     2. Check page0.HasSpace(30) → false
//     3. Check page1.HasSpace(30) → true
//     4. Add to page1 → now 49 records
//     5. Write pages back
//
//   After:
//     Page 0: 50 records (unchanged)
//     Page 1: 49 records (inserted here)
//
// If file doesn't exist, creates new file with one page containing this record.
func InsertRecord(path string, record MovieRecord, nullBitmap uint8) error {
	// encode the record to bytes
	encoded := EncodeRecord(record.MovieId, record.Title, record.Genres)
	recordLen := len(encoded)

	// try to open existing file
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			// file doesn't exist, create new file with this record
			return createNewFile(path, encoded, nullBitmap)
		}
		return err
	}
	defer f.Close()

	// read total count (first 4 bytes)
	var totalCount uint32
	if err := binary.Read(f, binary.LittleEndian, &totalCount); err != nil {
		return err
	}

	// read all existing pages into memory
	var pages []*Page
	for {
		pageData := make([]byte, PageSize)
		_, readErr := io.ReadFull(f, pageData)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}

		page, err := DecodePage(pageData)
		if err != nil {
			return err
		}
		pages = append(pages, page)
	}
	f.Close()

	// find first page with space
	inserted := false
	for _, page := range pages {
		if page.HasSpace(recordLen) {
			page.AddRecord(encoded, nullBitmap)
			inserted = true
			break
		}
	}

	// no page has space, create new page
	if !inserted {
		newPage := NewPage(uint32(len(pages)))
		if !newPage.AddRecord(encoded, nullBitmap) {
			return errors.New("record too large for page")
		}
		pages = append(pages, newPage)
	}

	// write all pages back to file
	f, err = os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// write total count (updated)
	totalCount = uint32(0)
	for _, p := range pages {
		totalCount += uint32(p.Header.RecordCount)
	}
	if err := binary.Write(f, binary.LittleEndian, totalCount); err != nil {
		return err
	}

	// write each page
	for _, page := range pages {
		if _, err := f.Write(page.Encode()); err != nil {
			return err
		}
	}

	return nil
}

// createNewFile creates a new binary file with one page containing the record.
func createNewFile(path string, record []byte, nullBitmap uint8) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// write count = 1
	if err := binary.Write(f, binary.LittleEndian, uint32(1)); err != nil {
		return err
	}

	// create page and add record
	page := NewPage(0)
	page.AddRecord(record, nullBitmap)

	// write page
	_, err = f.Write(page.Encode())
	return err
}

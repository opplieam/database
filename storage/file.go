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

// File wraps os.File for slotted page storage.
//
// File format:
//
//	[4 bytes] page count (uint32, little-endian)
//	[4096 bytes] page 0
//	[4096 bytes] page 1
//	...
type File struct {
	file      *os.File
	path      string
	pageCount uint32
}

// Open opens an existing slotted page file.
func Open(path string) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	// read page count
	var pageCount uint32
	if err := binary.Read(f, binary.LittleEndian, &pageCount); err != nil {
		f.Close()
		return nil, err
	}

	return &File{
		file:      f,
		path:      path,
		pageCount: pageCount,
	}, nil
}

// Create creates a new empty slotted page file.
func Create(path string) (*File, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	// write page count = 0
	if err := binary.Write(f, binary.LittleEndian, uint32(0)); err != nil {
		f.Close()
		return nil, err
	}

	return &File{
		file:      f,
		path:      path,
		pageCount: 0,
	}, nil
}

// ReadPage reads a single page by ID.
func (f *File) ReadPage(pageId uint32) (*Page, error) {
	if pageId >= f.pageCount {
		return nil, errors.New("page id out of bounds")
	}

	// seek to page position: 4 bytes header + pageId * PageSize
	offset := 4 + int64(pageId)*PageSize
	if _, err := f.file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	// read page data
	pageData := make([]byte, PageSize)
	if _, err := io.ReadFull(f.file, pageData); err != nil {
		return nil, err
	}

	return DecodePage(pageData)
}

// WritePage writes a single page by ID.
func (f *File) WritePage(pageId uint32, page *Page) error {
	if pageId >= f.pageCount {
		return errors.New("page id out of bounds")
	}

	// seek to page position
	offset := 4 + int64(pageId)*PageSize
	if _, err := f.file.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	// write page data
	_, err := f.file.Write(page.Encode())
	return err
}

// PageCount returns the number of pages.
func (f *File) PageCount() uint32 {
	return f.pageCount
}

// AppendPage adds a new page at the end.
func (f *File) AppendPage(page *Page) error {
	// seek to end
	if _, err := f.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	// write page data
	if _, err := f.file.Write(page.Encode()); err != nil {
		return err
	}

	// update page count
	f.pageCount++

	// rewrite page count at beginning
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(f.file, binary.LittleEndian, f.pageCount); err != nil {
		return err
	}

	return nil
}

// Close closes the file.
func (f *File) Close() error {
	return f.file.Close()
}

// WriteMoviesPages writes movies using slotted pages with null bitmaps.
func WriteMoviesPages(path string, movies []MovieRecord) error {
	file, err := Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, m := range movies {
		encoded := EncodeMovieRecord(m.MovieId, m.Title, m.Genres)
		var nullBitmap uint8

		page := NewPage(file.PageCount())
		if !page.AddRecord(encoded, nullBitmap) {
			return errors.New("record too large for page")
		}

		if err := file.AppendPage(page); err != nil {
			return err
		}
	}

	return nil
}

// ReadMoviesPages reads movies from slotted pages.
func ReadMoviesPages(path string) ([]MovieRecord, error) {
	file, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	movies := make([]MovieRecord, 0)

	for i := uint32(0); i < file.PageCount(); i++ {
		page, err := file.ReadPage(i)
		if err != nil {
			return nil, err
		}

		for j := 0; j < int(page.Header.RecordCount); j++ {
			recordBytes, err := page.GetRecord(j)
			if err != nil {
				return nil, err
			}

			nullBitmap, err := page.GetNullBitmap(j)
			if err != nil {
				return nil, err
			}

			id, title, genres, err := DecodeMovieRecord(recordBytes)
			if err != nil {
				return nil, err
			}

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

// InsertRecord inserts pre-encoded bytes into a slotted page file.
//
// If file doesn't exist, creates new file with one page containing this record.
// Uses FSM for O(1) page lookup instead of scanning all pages.
//
// Callers encode their data before calling:
//   - Raw records: EncodeMovieRecord(movieId, title, genres)
//   - MVCC records: EncodeTxRecord(txRec)
func InsertRecord(path string, encoded []byte, nullBitmap uint8) error {
	recordLen := len(encoded)

	// try to open existing file
	file, err := Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createNewFile(path, encoded, nullBitmap)
		}
		return err
	}
	defer file.Close()

	// open or create FSM
	fsmPath := path + ".fsm"
	fsm, err := OpenFSM(fsmPath)
	if err != nil {
		fsm, err = CreateFSM(fsmPath)
		if err != nil {
			return err
		}
		// initialize FSM with existing pages
		for i := uint32(0); i < file.PageCount(); i++ {
			page, err := file.ReadPage(i)
			if err != nil {
				fsm.Close()
				return err
			}
			fsm.AddPage(uint16(page.GetFreeSpace()))
		}
	}
	defer fsm.Close()

	// find page with space using FSM
	if pageId, ok := fsm.FindPageWithSpace(recordLen); ok {
		page, err := file.ReadPage(pageId)
		if err != nil {
			return err
		}
		page.AddRecord(encoded, nullBitmap)
		if err := file.WritePage(pageId, page); err != nil {
			return err
		}
		// TODO: In real databases (PostgreSQL), FSM is updated lazily:
		// - INSERT marks page as dirty in buffer pool
		// - CHECKPOINT writes dirty pages + updates FSM
		// - VACUUM scans dead tuples and updates FSM
		// Our implementation updates immediately for simplicity.
		fsm.Update(pageId, uint16(page.GetFreeSpace()))
		return fsm.save()
	}

	// no space, append new page
	page := NewPage(file.PageCount())
	if !page.AddRecord(encoded, nullBitmap) {
		return errors.New("record too large for page")
	}
	if err := file.AppendPage(page); err != nil {
		return err
	}
	// TODO: Same as above - FSM updated immediately for simplicity.
	// Real databases handle this during CHECKPOINT/VACUUM.
	fsm.AddPage(uint16(page.GetFreeSpace()))
	return fsm.save()
}

// createNewFile creates a new file with one page containing the record.
func createNewFile(path string, record []byte, nullBitmap uint8) error {
	file, err := Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	page := NewPage(0)
	page.AddRecord(record, nullBitmap)
	if err := file.AppendPage(page); err != nil {
		return err
	}

	// create FSM for new file
	fsmPath := path + ".fsm"
	fsm, err := CreateFSM(fsmPath)
	if err != nil {
		return err
	}
	defer fsm.Close()

	fsm.AddPage(uint16(page.GetFreeSpace()))
	return fsm.save()
}

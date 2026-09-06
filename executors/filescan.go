package executors

import (
	"database/storage"
	"encoding/csv"
	"io"
	"os"
)

// FileScan reads a CSV file and yields one row at a time as tuples.
//
// Example CSV:
//   movieId,title,genres
//   1,Toy Story (1995),Adventure|Animation
//   2,Jumanji (1995),Adventure|Children
//
// State transitions:
//
// Initial state:
//   f.headers = ["movieId", "title", "genres"]
//   f.done = false
//
// Call 1: Next()
//   done=false → read CSV row
//   record = ["1", "Toy Story (1995)", "Adventure|Animation"]
//   return Tuple{"1", "Toy Story (1995)", "Adventure|Animation"}
//
// Call 2: Next()
//   done=false → read CSV row
//   record = ["2", "Jumanji (1995)", "Adventure|Children"]
//   return Tuple{"2", "Jumanji (1995)", "Adventure|Children"}
//
// Call 3: Next()
//   done=false → read CSV row → io.EOF
//   done=true
//   return nil, io.EOF
//
// Note: All values are strings (CSV has no type info).
// Convert to int/uint32 when needed (e.g., movieId).
type FileScan struct {
	reader  *csv.Reader
	file    *os.File
	headers []string
	next    []string
	done    bool
}

// NewFileScan opens a CSV file, reads the header row, and returns a FileScan.
func NewFileScan(path string) (*FileScan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true

	// read headers
	headers, err := r.Read()
	if err != nil {
		f.Close()
		return nil, err
	}

	return &FileScan{
		reader:  r,
		file:    f,
		headers: headers,
	}, nil
}

// Next returns the next row as a Tuple, or io.EOF when exhausted.
func (f *FileScan) Next() (storage.Tuple, error) {
	if f.done {
		return nil, io.EOF
	}

	record, err := f.reader.Read()
	if err == io.EOF {
		f.done = true
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}

	// return all fields as a tuple
	t := make(storage.Tuple, len(record))
	for i, v := range record {
		t[i] = v
	}
	return t, nil
}

// Close closes the underlying file.
func (f *FileScan) Close() error {
	return f.file.Close()
}

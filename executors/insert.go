package executors

import (
	"io"
	"database/storage"
)

// Insert is an executor node that inserts one record into a binary file.
//
// Implements the Node interface:
//   - Next() inserts the record and returns it as a Tuple
//   - After first call, returns io.EOF
//
// Example:
//   insert := NewInsert("movies.data", storage.MovieRecord{999, "New Movie", "Action"}, 0)
//   tuple, err := insert.Next()
//   // tuple = {999, "New Movie", "Action"}, record is now in file
//
//   tuple, err = insert.Next()
//   // tuple = nil, err = io.EOF
type Insert struct {
	path   string
	record storage.MovieRecord
	bitmap uint8
	done   bool
}

// NewInsert creates a new Insert executor node.
func NewInsert(path string, record storage.MovieRecord, bitmap uint8) *Insert {
	return &Insert{
		path:   path,
		record: record,
		bitmap: bitmap,
	}
}

// Next inserts the record and returns it as a Tuple.
//
// State transitions:
//   Call 1: done=false → InsertRecord → done=true → return Tuple
//   Call 2: done=true → return io.EOF
func (i *Insert) Next() (storage.Tuple, error) {
	if i.done {
		return nil, io.EOF
	}

	if err := storage.InsertRecord(i.path, i.record, i.bitmap); err != nil {
		return nil, err
	}

	i.done = true
	return storage.Tuple{i.record.MovieId, i.record.Title, i.record.Genres}, nil
}

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
// When a TransactionContext is provided:
//   - Stores TxRecord with MVCC metadata (TxMin, TxMax, CID)
//   - Automatically logs commit to clog
//
// Example:
//
//	ctx := &TransactionContext{Tx: tx1, Clog: clog, Snapshot: tx1.Id()}
//	insert := NewInsert("movies.data", storage.MovieRecord{999, "New Movie", "Action"}, 0, ctx)
//	tuple, err := insert.Next()
//	// tuple = {999, "New Movie", "Action"}, record is now in file with MVCC metadata
type Insert struct {
	path   string
	record storage.MovieRecord
	bitmap uint8
	ctx    *TransactionContext
	done   bool
}

// NewInsert creates a new Insert executor node.
func NewInsert(path string, record storage.MovieRecord, bitmap uint8, ctx *TransactionContext) *Insert {
	return &Insert{
		path:   path,
		record: record,
		bitmap: bitmap,
		ctx:    ctx,
	}
}

// Next inserts the record and returns it as a Tuple.
//
// State transitions:
//
//	Call 1: done=false → InsertRecord → done=true → return Tuple
//	Call 2: done=true → return io.EOF
func (i *Insert) Next() (storage.Tuple, error) {
	if i.done {
		return nil, io.EOF
	}

	var encoded []byte

	if i.ctx != nil {
		// MVCC mode: store TxRecord with metadata
		cid := i.ctx.Tx.NextCID()
		txRec := storage.InsertTxRecord(i.ctx.Tx.Id(), cid, i.data())
		encoded = storage.EncodeTxRecord(txRec)
	} else {
		// Legacy mode: store raw MovieRecord
		encoded = storage.EncodeMovieRecord(i.record.MovieId, i.record.Title, i.record.Genres)
	}

	if err := storage.InsertRecord(i.path, encoded, i.bitmap); err != nil {
		return nil, err
	}

	// auto-log commit if MVCC mode
	if i.ctx != nil {
		if err := i.ctx.Clog.LogCommit(i.ctx.Tx.Id()); err != nil {
			return nil, err
		}
	}

	i.done = true
	return i.data(), nil
}

// data returns the tuple data from the record.
func (i *Insert) data() storage.Tuple {
	return storage.Tuple{i.record.MovieId, i.record.Title, i.record.Genres}
}

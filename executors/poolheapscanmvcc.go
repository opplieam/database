package executors

import (
	"errors"
	"io"

	"database/buffer"
	"database/storage"
)

// PoolHeapScanMVCC reads a binary file with slotted pages through a buffer
// pool, yielding one record at a time.
//
// Each Next call borrows the current page, reads one record, and returns
// the page before yielding. No pin survives a Next return, so abandoning
// the scan (e.g. under Limit) leaks nothing.
//
// ctx is required: every record decodes as a TxRecord with MVCC metadata
// (TxMin, TxMax, CID), and only tuples visible to the transaction return.
// There is no legacy raw-decode mode.
//
// MVCC decoding mirrors HeapFileScan.Next in MVCC mode. If one changes,
// the other must follow until the pool path replaces the legacy one.
//
// Example state transitions for a file with 3 records (page 0 has 2, page 1 has 1):
//
// Initial state:
//
//	s.currentPage = 0, s.recordIdx = 0, s.done = false
//
// Call 1: Next()
//
//	pool.Get(page 0) -> miss, read record 0, pool.Unpin(page 0)
//	recordIdx becomes 1
//	return Tuple{1, "Toy Story", "Adventure"}
//
// Call 2: Next()
//
//	pool.Get(page 0) -> hit, read record 1, pool.Unpin(page 0)
//	recordIdx becomes 2
//	return Tuple{2, "Jumanji", "Adventure"}
//
// Call 3: Next()
//
//	pool.Get(page 0) -> hit, recordIdx 2 >= RecordCount 2, Unpin, advance
//	pool.Get(page 1) -> miss, read record 0, Unpin
//	return Tuple{3, "Grumpier Old Men", "Comedy"}
//
// Call 4: Next()
//
//	currentPage 2 >= pageTotal 2 -> done, return nil, io.EOF
type PoolHeapScanMVCC struct {
	path        string
	pool        *buffer.BufferPool
	pageTotal   int
	currentPage int
	recordIdx   int
	done        bool
	ctx         *TransactionContext
}

// NewPoolHeapScanMVCC scans a binary file through a buffer pool with MVCC
// visibility filtering.
//
// pageTotal is read once from the file header; every page read goes
// through pool.Get/Unpin per record, so all scans share the cache.
func NewPoolHeapScanMVCC(path string, ctx *TransactionContext, pool *buffer.BufferPool) (*PoolHeapScanMVCC, error) {
	if pool == nil {
		return nil, errors.New("executors: NewPoolHeapScanMVCC requires a non-nil pool")
	}
	if ctx == nil {
		return nil, errors.New("executors: NewPoolHeapScanMVCC requires a non-nil TransactionContext")
	}
	f, err := storage.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return &PoolHeapScanMVCC{
		path:      path,
		pool:      pool,
		pageTotal: int(f.PageCount()),
		ctx:       ctx,
	}, nil
}

// Next returns the next visible record as a Tuple, or io.EOF when exhausted.
// Each record decodes as a TxRecord and invisible ones are skipped.
func (s *PoolHeapScanMVCC) Next() (storage.Tuple, error) {
	if s.done {
		return nil, io.EOF
	}

	for {
		if s.currentPage >= s.pageTotal {
			s.done = true
			return nil, io.EOF
		}

		// Step 1: borrow the current page for one record.
		tag := buffer.BufferTag{Path: s.path, PageId: uint32(s.currentPage)}
		page, err := s.pool.Get(tag)
		if err != nil {
			return nil, err
		}

		// Step 2: unread records remain? Read one, return the page.
		if s.recordIdx < int(page.Header.RecordCount) {
			recordBytes, err := page.GetRecord(s.recordIdx)
			if err != nil {
				_ = s.pool.Unpin(tag)
				return nil, err
			}
			s.recordIdx++
			if err := s.pool.Unpin(tag); err != nil {
				return nil, err
			}

			// Step 3: visible? Yield it; invisible? Loop for the next record.
			txRec, err := storage.DecodeTxRecord(recordBytes)
			if err != nil {
				return nil, err
			}
			if txRec.Visible(s.ctx.Snapshot, s.ctx.Clog, s.ctx.XipList) {
				return txRec.Data, nil
			}
			// skip invisible record, continue loop
		} else {
			// Step 4: page exhausted: return it, advance to the next page.
			if err := s.pool.Unpin(tag); err != nil {
				return nil, err
			}
			s.currentPage++
			s.recordIdx = 0
		}
	}
}

// Close marks the scan done. Pages are returned per record, so no pins
// are held here.
func (s *PoolHeapScanMVCC) Close() error {
	s.done = true
	return nil
}

package executors

import (
	"database/storage"
	"encoding/binary"
	"io"
	"os"
)

// HeapFileScan reads a binary file with slotted pages and yields one record at a time.
//
// When a TransactionContext is provided:
//   - Decodes TxRecord with MVCC metadata (TxMin, TxMax, CID)
//   - Only returns tuples visible to the current transaction
//
// When ctx is nil (legacy mode):
//   - Decodes raw MovieRecord without MVCC filtering
//
// Example state transitions for a file with 3 records (page 0 has 2, page 1 has 1):
//
// Initial state:
//
//	h.page = nil, h.recordIdx = 0, h.done = false
//
// Call 1: Next()
//
//	h.page is nil → read page 0 from file
//	page 0 has RecordCount=2
//	h.page = page0, h.recordIdx = 0
//	0 < 2 → true, get record 0, recordIdx becomes 1
//	return Tuple{1, "Toy Story", "Adventure"}
//
// Call 2: Next()
//
//	h.page = page0, h.recordIdx = 1, RecordCount = 2
//	1 < 2 → true, get record 1, recordIdx becomes 2
//	return Tuple{2, "Jumanji", "Adventure"}
//
// Call 3: Next()
//
//	h.page = page0, h.recordIdx = 2, RecordCount = 2
//	2 < 2 → false, read next page (page 1)
//	page 1 has RecordCount=1
//	h.page = page1, h.recordIdx = 0
//	0 < 1 → true, get record 0, recordIdx becomes 1
//	return Tuple{3, "Grumpier Old Men", "Comedy"}
//
// Call 4: Next()
//
//	h.page = page1, h.recordIdx = 1, RecordCount = 1
//	1 < 1 → false, try read next page
//	io.EOF → no more pages
//	h.done = true, return nil, io.EOF
type HeapFileScan struct {
	file       *os.File
	pageCount  uint32
	currentPage int
	page       *storage.Page
	recordIdx  int
	done       bool
	ctx        *TransactionContext
}

// NewHeapFileScan opens a binary file and reads the record count from the first 4 bytes.
func NewHeapFileScan(path string, ctx *TransactionContext) (*HeapFileScan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	// read record count (first 4 bytes)
	// Example: file starts with [0x4E 0x6B 0x00 0x00] = 27278 in little-endian
	var count uint32
	if err := binary.Read(f, binary.LittleEndian, &count); err != nil {
		f.Close()
		return nil, err
	}

	return &HeapFileScan{
		file:      f,
		pageCount: count,
		ctx:       ctx,
	}, nil
}

// Next returns the next visible record as a Tuple, or io.EOF when exhausted.
//
// In MVCC mode (ctx != nil):
//   - Decodes TxRecord
//   - Checks visibility using TxRecord.Visible()
//   - Skips invisible records
//
// In legacy mode (ctx == nil):
//   - Decodes raw MovieRecord
func (h *HeapFileScan) Next() (storage.Tuple, error) {
	if h.done {
		return nil, io.EOF
	}

	// try to get next record from current page
	for {
		if h.page != nil && h.recordIdx < int(h.page.Header.RecordCount) {
			recordBytes, err := h.page.GetRecord(h.recordIdx)
			if err != nil {
				return nil, err
			}
			h.recordIdx++

			if h.ctx != nil {
				// MVCC mode: decode TxRecord and check visibility
				txRec, err := storage.DecodeTxRecord(recordBytes)
				if err != nil {
					return nil, err
				}
				if txRec.Visible(h.ctx.Snapshot, h.ctx.Clog, h.ctx.XipList) {
					return txRec.Data, nil
				}
				// skip invisible record, continue loop
			} else {
				// Legacy mode: decode raw MovieRecord
				id, title, genres, err := storage.DecodeMovieRecord(recordBytes)
				if err != nil {
					return nil, err
				}
				return storage.Tuple{id, title, genres}, nil
			}
		}

		// read next page (4096 bytes)
		pageData := make([]byte, storage.PageSize)
		_, err := io.ReadFull(h.file, pageData)
		if err == io.EOF {
			h.done = true
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}

		page, err := storage.DecodePage(pageData)
		if err != nil {
			return nil, err
		}

		h.page = page
		h.recordIdx = 0
		h.currentPage++
	}
}

func (h *HeapFileScan) Close() error {
	return h.file.Close()
}

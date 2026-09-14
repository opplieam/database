package executors

import (
	"database/storage"
	"encoding/binary"
	"io"
	"os"
)

// HeapFileScan reads a binary file with slotted pages and yields one record at a time.
//
// Example state transitions for a file with 3 records (page 0 has 2, page 1 has 1):
//
// Initial state:
//   h.page = nil, h.recordIdx = 0, h.done = false
//
// Call 1: Next()
//   h.page is nil → read page 0 from file
//   page 0 has RecordCount=2
//   h.page = page0, h.recordIdx = 0
//   0 < 2 → true, get record 0, recordIdx becomes 1
//   return Tuple{1, "Toy Story", "Adventure"}
//
// Call 2: Next()
//   h.page = page0, h.recordIdx = 1, RecordCount = 2
//   1 < 2 → true, get record 1, recordIdx becomes 2
//   return Tuple{2, "Jumanji", "Adventure"}
//
// Call 3: Next()
//   h.page = page0, h.recordIdx = 2, RecordCount = 2
//   2 < 2 → false, read next page (page 1)
//   page 1 has RecordCount=1
//   h.page = page1, h.recordIdx = 0
//   0 < 1 → true, get record 0, recordIdx becomes 1
//   return Tuple{3, "Grumpier Old Men", "Comedy"}
//
// Call 4: Next()
//   h.page = page1, h.recordIdx = 1, RecordCount = 1
//   1 < 1 → false, try read next page
//   io.EOF → no more pages
//   h.done = true, return nil, io.EOF
type HeapFileScan struct {
	file       *os.File
	pageCount  uint32
	currentPage int
	page       *storage.Page
	recordIdx  int
	done       bool
	xipList    map[uint64]bool
}

// NewHeapFileScan opens a binary file and reads the record count from the first 4 bytes.
func NewHeapFileScan(path string, xipList map[uint64]bool) (*HeapFileScan, error) {
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
		xipList:   xipList,
	}, nil
}

// Next returns the next record as a Tuple, or io.EOF when exhausted.
//
// Step-by-step for call N:
// 1. If h.done → return EOF (already exhausted)
// 2. If current page has records left (recordIdx < RecordCount):
//    - Get record bytes from page at recordIdx
//    - Increment recordIdx
//    - Decode bytes → Tuple{id, title, genres}
//    - Return Tuple
// 3. Otherwise, read next 4096 bytes from file:
//    - If io.EOF → set done=true, return EOF
//    - Decode bytes → Page
//    - Set h.page = new page, reset recordIdx = 0
//    - Loop back to step 2
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

			// decode record into tuple
			// Example: recordBytes = [01 00 00 00 09 54 6F 79 20 53 74 6F 72 79 09 41 64 76 65 6E 74 75 72 65]
			//         DecodeRecord → id=1, title="Toy Story", genres="Adventure"
			id, title, genres, err := storage.DecodeRecord(recordBytes)
			if err != nil {
				return nil, err
			}
			return storage.Tuple{id, title, genres}, nil
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

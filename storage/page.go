package storage

import (
	"encoding/binary"
	"errors"
)

const PageSize = 4096

// Page layout:
// ┌─────────────────────────┐ ← offset 0
// │ Page Header (8 bytes)   │
// │   - page id (4 bytes)   │
// │   - record count (2)    │
// │   - free offset (2)     │
// ├─────────────────────────┤
// │ Line Pointers           │ → grow forward →
// │   - offset (2 bytes)    │
// │   - length (2 bytes)    │
// ├─────────────────────────┤
// │                         │
// │   Free Space            │
// │                         │
// ├─────────────────────────┤
// │ Records                 │ ← grow backward ←
// └─────────────────────────┘

type PageHeader struct {
	PageId     uint32
	RecordCount uint16
	FreeOffset uint16
}

type LinePointer struct {
	Offset uint16
	Length uint16
}

type Page struct {
	Header       PageHeader
	LinePointers []LinePointer
	NullBitmaps  []uint8 // one byte per record, bits represent null columns
	Records      []byte
	data         [PageSize]byte
}

// NewPage creates a new empty page with the given id.
func NewPage(pageId uint32) *Page {
	return &Page{
		Header: PageHeader{
			PageId:     pageId,
			RecordCount: 0,
			FreeOffset: 8, // after header
		},
		LinePointers: nil,
		Records:      nil,
	}
}

// AddRecord adds a record to the page with null bitmap. Returns false if not enough space.
//
// Example: adding record with nullBitmap = 2 (0b010 = title is NULL)
//
// Before:
//   FreeOffset = 8 (just header)
//   Records = empty
//
// After:
//   FreeOffset = 13 (8 + 4 line pointer + 1 null bitmap)
//   LinePointers = [{offset:4074, length:22}]
//   NullBitmaps = [2]
//   Records = [22 bytes of record data]
//
// The null bitmap tells us which columns are NULL:
//   bit 0 = movieId NULL
//   bit 1 = title NULL
//   bit 2 = genres NULL
func (p *Page) AddRecord(record []byte, nullBitmap uint8) bool {
	recordLen := uint16(len(record))
	linePtrSize := uint16(4) // each line pointer is 4 bytes (offset + length)
	nullBitmapSize := uint16(1) // 1 byte for null bitmap

	// check if we have space: freeOffset + linePtrSize + nullBitmapSize + recordLen <= PageSize
	// records grow backward from end of page
	recordsStart := uint16(PageSize) - uint16(len(p.Records)) - recordLen
	newFreeOffset := p.Header.FreeOffset + linePtrSize + nullBitmapSize

	if newFreeOffset > recordsStart {
		return false
	}

	// add line pointer
	p.LinePointers = append(p.LinePointers, LinePointer{
		Offset: recordsStart,
		Length: recordLen,
	})

	// add null bitmap
	p.NullBitmaps = append(p.NullBitmaps, nullBitmap)

	// add record (at the beginning of Records, growing backward)
	p.Records = append(record, p.Records...)

	// update header
	p.Header.RecordCount++
	p.Header.FreeOffset = newFreeOffset

	return true
}

// GetNullBitmap returns the null bitmap for the record at the given index.
func (p *Page) GetNullBitmap(index int) (uint8, error) {
	if index < 0 || index >= int(p.Header.RecordCount) {
		return 0, errors.New("record index out of bounds")
	}
	return p.NullBitmaps[index], nil
}

// IsNull checks if a column is NULL in the given record.
// column is 0-indexed.
//
// Example: nullBitmap = 0b010 (decimal 2)
//   IsNull(nullBitmap, 0) → bit 0 = 0 → false (movieId not NULL)
//   IsNull(nullBitmap, 1) → bit 1 = 1 → true  (title IS NULL)
//   IsNull(nullBitmap, 2) → bit 2 = 0 → false (genres not NULL)
//
// How the check works:
//   1 << column creates a mask: column 0 → 0b001, column 1 → 0b010, column 2 → 0b100
//   nullBitmap & mask: if result != 0, that bit is set → column is NULL
//
// Example for column 1 (title):
//   nullBitmap = 0b010
//   mask = 1 << 1 = 0b010
//   0b010 & 0b010 = 0b010 ≠ 0 → true (NULL)
func IsNull(nullBitmap uint8, column int) bool {
	return nullBitmap&(1<<uint(column)) != 0
}

// HasSpace checks if the page has room for a new record of the given length.
//
// To add a record, we need space for:
//   - 1 line pointer (4 bytes): offset + length
//   - 1 null bitmap (1 byte): which columns are NULL
//   - the record itself (recordLen bytes)
//
// Layout grows from both ends:
//   Line pointers + null bitmaps → grow forward from offset 8
//   Records → grow backward from offset 4096
//
// They meet in the middle = free space. If they would overlap, page is full.
//
// Example: page with 48 records, each ~30 bytes
//   FreeOffset = 8 + (48 × 5) = 248 bytes (header + line ptrs + null bitmaps)
//   Records = 48 × 30 = 1440 bytes
//   Records start at 4096 - 1440 = 2656
//   Free space = 2656 - 248 = 2408 bytes
//   HasSpace(30) → 248 + 5 + 30 = 283 ≤ 2656 → true
//
// Example: page with 50 records (full)
//   FreeOffset = 8 + (50 × 5) = 258
//   Records = 50 × 30 = 1500
//   Records start at 4096 - 1500 = 2596
//   Free space = 2596 - 258 = 2338
//   HasSpace(30) → 258 + 5 + 30 = 293 ≤ 2596 → true (still fits)
//
// Example: page with 51 records (truly full)
//   FreeOffset = 8 + (51 × 5) = 263
//   Records = 51 × 30 = 1530
//   Records start at 4096 - 1530 = 2566
//   HasSpace(30) → 263 + 5 + 30 = 298 ≤ 2566 → true
//
// Wait, 50 records at 30 bytes each = 1500. 4096 - 1500 = 2596.
// Line pointers: 50 × 4 = 200. Null bitmaps: 50 × 1 = 50. Total: 250 + 8 = 258.
// 258 + 5 = 263. 263 + 30 = 293. 293 ≤ 2596 → true. Still fits!
//
// Actually 50 records fits because each record is 30 bytes, and line ptr + null bitmap is 5 bytes.
// So we can fit about 4096 / 35 ≈ 117 records per page (theoretical max).
func (p *Page) HasSpace(recordLen int) bool {
	linePtrSize := 4
	nullBitmapSize := 1
	needed := linePtrSize + nullBitmapSize + recordLen

	recordsStart := PageSize - len(p.Records) - recordLen
	return int(p.Header.FreeOffset)+needed <= recordsStart
}

// GetFreeSpace returns the number of bytes available for new records.
//
// Free space = where records start - where metadata ends
//
// Example: page with 48 records of 30 bytes each
//   FreeOffset = 8 + (48 × 5) = 248
//   Records = 48 × 30 = 1440
//   Records start at 4096 - 1440 = 2656
//   Free space = 2656 - 248 = 2408 bytes
//
// Example: empty page
//   FreeOffset = 8 (just header)
//   Records = 0
//   Records start at 4096
//   Free space = 4096 - 8 = 4088 bytes
func (p *Page) GetFreeSpace() int {
	recordsStart := PageSize - len(p.Records)
	return recordsStart - int(p.Header.FreeOffset)
}

// GetRecord returns the record at the given index.
func (p *Page) GetRecord(index int) ([]byte, error) {
	if index < 0 || index >= int(p.Header.RecordCount) {
		return nil, errors.New("record index out of bounds")
	}

	lp := p.LinePointers[index]
	record := make([]byte, lp.Length)
	copy(record, p.Records[lp.Offset-uint16(PageSize-len(p.Records)):])
	return record, nil
}

// Encode serializes the page to bytes.
//
// On-disk layout (4096 bytes total):
// ┌─────────────────────────────────────────────────────────────┐
// │ Page Header (8 bytes)                                       │
// │   [0-3]  PageId = 0x00000000                                │
// │   [4-5]  RecordCount = 0x0002 (2 records)                   │
// │   [6-7]  FreeOffset = 0x0012 (18 = 8 + 4×2 + 1×2)          │
// ├─────────────────────────────────────────────────────────────┤
// │ Line Pointers (8 bytes for 2 records)                       │
// │   [8-11]  Record 0: offset=4074, length=22                  │
// │   [12-15] Record 1: offset=4052, length=20                  │
// ├─────────────────────────────────────────────────────────────┤
// │ Null Bitmaps (2 bytes for 2 records)                        │
// │   [16] Record 0: nullBitmap=0 (no NULLs)                    │
// │   [17] Record 1: nullBitmap=2 (title is NULL)               │
// ├─────────────────────────────────────────────────────────────┤
// │ Free Space (4052-16 = 4036 bytes)                           │
// ├─────────────────────────────────────────────────────────────┤
// │ Records (grow backward from end)                            │
// │   [4052-4072] Record 1 (20 bytes)                           │
// │   [4074-4096] Record 0 (22 bytes)                           │
// └─────────────────────────────────────────────────────────────┘
func (p *Page) Encode() []byte {
	data := make([]byte, PageSize)
	offset := 0

	// write header
	binary.LittleEndian.PutUint32(data[offset:], p.Header.PageId)
	offset += 4
	binary.LittleEndian.PutUint16(data[offset:], p.Header.RecordCount)
	offset += 2
	binary.LittleEndian.PutUint16(data[offset:], p.Header.FreeOffset)
	offset += 2

	// write line pointers
	for _, lp := range p.LinePointers {
		binary.LittleEndian.PutUint16(data[offset:], lp.Offset)
		offset += 2
		binary.LittleEndian.PutUint16(data[offset:], lp.Length)
		offset += 2
	}

	// write null bitmaps
	for _, nullBitmap := range p.NullBitmaps {
		data[offset] = nullBitmap
		offset++
	}

	// write records (at end of page, growing backward)
	copy(data[PageSize-len(p.Records):], p.Records)

	return data
}

// DecodePage deserializes a page from bytes.
func DecodePage(data []byte) (*Page, error) {
	if len(data) < PageSize {
		return nil, errors.New("data too short for page")
	}

	p := &Page{}
	offset := 0

	// read header
	p.Header.PageId = binary.LittleEndian.Uint32(data[offset:])
	offset += 4
	p.Header.RecordCount = binary.LittleEndian.Uint16(data[offset:])
	offset += 2
	p.Header.FreeOffset = binary.LittleEndian.Uint16(data[offset:])
	offset += 2

	// read line pointers
	p.LinePointers = make([]LinePointer, p.Header.RecordCount)
	for i := uint16(0); i < p.Header.RecordCount; i++ {
		p.LinePointers[i].Offset = binary.LittleEndian.Uint16(data[offset:])
		offset += 2
		p.LinePointers[i].Length = binary.LittleEndian.Uint16(data[offset:])
		offset += 2
	}

	// read null bitmaps
	p.NullBitmaps = make([]uint8, p.Header.RecordCount)
	for i := uint16(0); i < p.Header.RecordCount; i++ {
		p.NullBitmaps[i] = data[offset]
		offset++
	}

	// read records
	if p.Header.RecordCount > 0 {
		// find the range of records
		minOffset := uint16(PageSize)
		maxEnd := uint16(0)
		for _, lp := range p.LinePointers {
			if lp.Offset < minOffset {
				minOffset = lp.Offset
			}
			end := lp.Offset + lp.Length
			if end > maxEnd {
				maxEnd = end
			}
		}
		p.Records = make([]byte, maxEnd-minOffset)
		copy(p.Records, data[minOffset:maxEnd])
	}

	return p, nil
}

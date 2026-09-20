package storage

import (
	"encoding/binary"
	"io"
	"os"
)

// FreeSpaceMap tracks free space per page for fast INSERT operations.
//
// File format:
//   [4 bytes] page count (uint32, little-endian)
//   [2 bytes] page 0 free space (uint16)
//   [2 bytes] page 1 free space (uint16)
//   ...
//
// Without FSM: INSERT scans all pages O(n)
// With FSM: INSERT does O(1) lookup
type FreeSpaceMap struct {
	file      *os.File
	path      string
	freeSpace []uint16  // index = pageId, value = free bytes
}

// OpenFSM opens an existing FSM file.
func OpenFSM(path string) (*FreeSpaceMap, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	fsm := &FreeSpaceMap{
		file: f,
		path: path,
	}

	if err := fsm.load(); err != nil {
		f.Close()
		return nil, err
	}

	return fsm, nil
}

// CreateFSM creates a new empty FSM file.
func CreateFSM(path string) (*FreeSpaceMap, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	fsm := &FreeSpaceMap{
		file:      f,
		path:      path,
		freeSpace: make([]uint16, 0),
	}

	// write page count = 0
	if err := binary.Write(f, binary.LittleEndian, uint32(0)); err != nil {
		f.Close()
		return nil, err
	}

	return fsm, nil
}

// Close closes the FSM file.
func (fsm *FreeSpaceMap) Close() error {
	return fsm.file.Close()
}

// load reads FSM data from disk into memory.
func (fsm *FreeSpaceMap) load() error {
	// seek to start
	if _, err := fsm.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// read page count
	var pageCount uint32
	if err := binary.Read(fsm.file, binary.LittleEndian, &pageCount); err != nil {
		return err
	}

	// read free space for each page
	fsm.freeSpace = make([]uint16, pageCount)
	for i := uint32(0); i < pageCount; i++ {
		if err := binary.Read(fsm.file, binary.LittleEndian, &fsm.freeSpace[i]); err != nil {
			return err
		}
	}

	return nil
}

// save writes FSM data from memory to disk.
func (fsm *FreeSpaceMap) save() error {
	// seek to start
	if _, err := fsm.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// write page count
	if err := binary.Write(fsm.file, binary.LittleEndian, uint32(len(fsm.freeSpace))); err != nil {
		return err
	}

	// write free space for each page
	for _, free := range fsm.freeSpace {
		if err := binary.Write(fsm.file, binary.LittleEndian, free); err != nil {
			return err
		}
	}

	return nil
}

// Get returns the free space for a page.
func (fsm *FreeSpaceMap) Get(pageId uint32) uint16 {
	if pageId >= uint32(len(fsm.freeSpace)) {
		return 0
	}
	return fsm.freeSpace[pageId]
}

// Update sets the free space for a page.
func (fsm *FreeSpaceMap) Update(pageId uint32, freeBytes uint16) {
	// extend slice if needed
	for pageId >= uint32(len(fsm.freeSpace)) {
		fsm.freeSpace = append(fsm.freeSpace, 0)
	}
	fsm.freeSpace[pageId] = freeBytes
}

// AddPage adds a new page to the FSM.
func (fsm *FreeSpaceMap) AddPage(freeBytes uint16) {
	fsm.freeSpace = append(fsm.freeSpace, freeBytes)
}

// PageCount returns the number of pages tracked.
func (fsm *FreeSpaceMap) PageCount() uint32 {
	return uint32(len(fsm.freeSpace))
}

// FindPageWithSpace returns the first page with enough free space.
// Returns (pageId, true) if found, (0, false) if not found.
func (fsm *FreeSpaceMap) FindPageWithSpace(size int) (uint32, bool) {
	for i, free := range fsm.freeSpace {
		if int(free) >= size {
			return uint32(i), true
		}
	}
	return 0, false
}

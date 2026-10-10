// Segment file layout:
//
//	[0-7] magic: "WAL1" + version, rejects non-WAL files at open
//	[8..] records packed back to back; LSN = byte offset from file start
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// Segment magic: fixed stamp plus format version, 8 bytes total. Open
// refuses files that do not start with these bytes.
var segmentStamp = []byte{'W', 'A', 'L', '1'}

const segmentVersion = 1

// SegmentHeaderSize is the magic length. Records start after it, so the
// first record's LSN is 8.
const SegmentHeaderSize = 8

// errTornTail marks an interrupted append: short bytes at the tip, never
// synced. Stops iteration, triggers truncation, never an error to callers.
var errTornTail = errors.New("torn tail")

// Segment is one append-only WAL file held open. LSNs are byte offsets;
// the first record starts after the magic. Single-threaded: no locks.
type Segment struct {
	file    *os.File
	end     LSN // next write offset; the LSN the next record gets
	lastLSN LSN // LSN of the most recent record, 0 if none
}

// Open opens or creates the segment at path and resumes at its end. A
// torn tail truncates back to the last good record: those bytes were
// never synced, so no reader could trust them, and leaving them would
// poison later appends.
func Open(path string) (*Segment, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	seg := &Segment{file: f, end: LSN(st.Size())}
	if st.Size() == 0 {
		if err := writeMagic(f); err != nil {
			f.Close()
			return nil, err
		}
		seg.end = SegmentHeaderSize
		return seg, nil
	}
	magic := make([]byte, SegmentHeaderSize)
	if _, err := f.ReadAt(magic, 0); err != nil {
		f.Close()
		return nil, err
	}
	if string(magic[:4]) != string(segmentStamp) ||
		binary.LittleEndian.Uint32(magic[4:]) != segmentVersion {
		f.Close()
		return nil, fmt.Errorf("wal: %s is not a WAL segment", path)
	}
	last, end, err := seg.scan()
	if err != nil {
		f.Close()
		return nil, err
	}
	seg.lastLSN, seg.end = last, end
	return seg, nil
}

// writeMagic writes the 8-byte stamp plus version to a fresh file.
func writeMagic(f *os.File) error {
	buf := make([]byte, SegmentHeaderSize)
	copy(buf, segmentStamp)
	binary.LittleEndian.PutUint32(buf[4:], segmentVersion)
	_, err := f.Write(buf)
	return err
}

// Append encodes rec at the current end and returns its LSN. The segment
// stamps the chain link: prev is the previous record's LSN, 0 for the
// first. Bytes reach the file before the LSN goes anywhere: a page never
// claims an LSN whose record is not already in the file.
func (s *Segment) Append(rec *Record) (LSN, error) {
	rec.PrevLSN = s.lastLSN
	buf := EncodeRecord(rec)
	lsn := s.end
	if _, err := s.file.WriteAt(buf, int64(lsn)); err != nil {
		return 0, err
	}
	s.lastLSN = lsn
	s.end = lsn + LSN(len(buf))
	return lsn, nil
}

// Flush syncs the file: bytes leave the OS cache for stable storage.
// Durability reduces to this call: without Sync, "flush" only reaches
// memory and the write-ahead ordering proves nothing.
func (s *Segment) Flush() error {
	return s.file.Sync()
}

// End returns the current log end: the LSN the next record will occupy.
// Checkpoint captures it as REDO. Read-only: only Append advances it.
func (s *Segment) End() LSN { return s.end }

// Iterate calls yield for each record from `from` to end of file, in
// order. A torn tail stops iteration cleanly; yield's error aborts and
// propagates. Early return from yield is the Ch 10 early-stop shape.
func (s *Segment) Iterate(from LSN, yield func(LSN, *Record) error) error {
	off := from
	for {
		rec, next, err := s.readOne(off)
		if err == errTornTail {
			return nil
		}
		if err != nil {
			return err
		}
		if err := yield(off, rec); err != nil {
			return err
		}
		off = next
	}
}

// Close flushes and closes the file. Callers close the pool first:
// pool close writes dirty pages, and the WAL flush must precede them.
func (s *Segment) Close() error {
	if err := s.Flush(); err != nil {
		return err
	}
	return s.file.Close()
}

// scan walks records from after the magic, returning the last good
// record's LSN and the end offset. Finds the resume point on open and
// the truncation point for a torn tail.
func (s *Segment) scan() (LSN, LSN, error) {
	off := LSN(SegmentHeaderSize)
	var last LSN
	for {
		_, next, err := s.readOne(off)
		if err == errTornTail {
			if terr := s.file.Truncate(int64(off)); terr != nil {
				return 0, 0, terr
			}
			return last, off, nil
		}
		if err != nil {
			return 0, 0, err
		}
		last = off
		off = next
	}
}

// readOne decodes the record at off, returning it and the next offset.
// Short bytes mean an interrupted append (errTornTail). Complete bytes
// with a bad checksum mean damage inside history, a real error.
func (s *Segment) readOne(off LSN) (*Record, LSN, error) {
	hdr := make([]byte, RecordHeaderSize)
	n, _ := s.file.ReadAt(hdr, int64(off))
	if n < RecordHeaderSize {
		return nil, 0, errTornTail
	}
	tot := binary.LittleEndian.Uint32(hdr[0:])
	if tot < RecordHeaderSize {
		return nil, 0, fmt.Errorf("wal: bad length %d at LSN %d", tot, off)
	}
	buf := make([]byte, tot)
	copy(buf, hdr)
	n, _ = s.file.ReadAt(buf[RecordHeaderSize:], int64(off)+RecordHeaderSize)
	if n < len(buf)-RecordHeaderSize {
		return nil, 0, errTornTail
	}
	rec, err := DecodeRecord(buf)
	if err != nil {
		return nil, 0, fmt.Errorf("wal: LSN %d: %w", off, err)
	}
	return rec, off + LSN(tot), nil
}

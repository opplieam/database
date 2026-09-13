package tx

import (
	"encoding/binary"
	"os"
)

// ClogStatus represents the status of a transaction in the commit log.
type ClogStatus byte

const (
	ClogStatusInProgress ClogStatus = 0x00
	ClogStatusCommitted  ClogStatus = 0x01
	ClogStatusAborted    ClogStatus = 0x02
)

// CommitLog tracks transaction status on disk.
//
// On-disk format (append-only):
//
//	[4 bytes] record count (little-endian uint32)
//	[N bytes] records: [8 bytes txId][1 byte status]
//
// TODO: Improve to buffer + flush for better performance.
// Current: write-through (every commit → disk)
// Future: in-memory buffer → periodic flush to disk
type CommitLog struct {
	file    *os.File
	records map[uint64]ClogStatus // cache for fast reads
}

// OpenCommitLog opens or creates a commit log file.
// Loads existing records from disk into memory.
func OpenCommitLog(path string) (*CommitLog, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	cl := &CommitLog{
		file:    f,
		records: make(map[uint64]ClogStatus),
	}

	if err := cl.load(); err != nil {
		f.Close()
		return nil, err
	}

	return cl, nil
}

// LogCommit records that a transaction committed.
func (cl *CommitLog) LogCommit(txId uint64) error {
	cl.records[txId] = ClogStatusCommitted
	return cl.append(txId, ClogStatusCommitted)
}

// LogAbort records that a transaction aborted.
func (cl *CommitLog) LogAbort(txId uint64) error {
	cl.records[txId] = ClogStatusAborted
	return cl.append(txId, ClogStatusAborted)
}

// IsCommitted returns true if the transaction committed.
func (cl *CommitLog) IsCommitted(txId uint64) bool {
	return cl.records[txId] == ClogStatusCommitted
}

// IsAborted returns true if the transaction aborted.
func (cl *CommitLog) IsAborted(txId uint64) bool {
	return cl.records[txId] == ClogStatusAborted
}

// IsInProgress returns true if the transaction is still in progress.
func (cl *CommitLog) IsInProgress(txId uint64) bool {
	status, ok := cl.records[txId]
	if !ok {
		return true // unknown = in progress
	}
	return status == ClogStatusInProgress
}

// Close closes the commit log file.
func (cl *CommitLog) Close() error {
	return cl.file.Close()
}

// append writes a record to the file.
func (cl *CommitLog) append(txId uint64, status ClogStatus) error {
	// seek to end
	if _, err := cl.file.Seek(0, os.SEEK_END); err != nil {
		return err
	}

	// write record: [8 bytes txId][1 byte status]
	buf := make([]byte, 9)
	binary.LittleEndian.PutUint64(buf[0:], txId)
	buf[8] = byte(status)

	_, err := cl.file.Write(buf)
	return err
}

// load reads all records from disk into memory.
func (cl *CommitLog) load() error {
	// seek to start
	if _, err := cl.file.Seek(0, os.SEEK_SET); err != nil {
		return err
	}

	// read count
	countBuf := make([]byte, 4)
	if _, err := cl.file.Read(countBuf); err != nil {
		// empty file is OK
		return nil
	}
	count := binary.LittleEndian.Uint32(countBuf)

	// read records
	for i := uint32(0); i < count; i++ {
		recBuf := make([]byte, 9)
		if _, err := cl.file.Read(recBuf); err != nil {
			break
		}
		txId := binary.LittleEndian.Uint64(recBuf[0:])
		status := ClogStatus(recBuf[8])
		cl.records[txId] = status
	}

	return nil
}

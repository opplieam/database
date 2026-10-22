package tx

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// clogRecordSize is the fixed width of one on-disk entry: an 8-byte
// transaction id followed by a 1-byte status.
const clogRecordSize = 8 + 1

// ClogStatus represents the status of a transaction in the commit log.
type ClogStatus byte

const (
	ClogStatusInProgress ClogStatus = 0x00
	ClogStatusCommitted  ClogStatus = 0x01
	ClogStatusAborted    ClogStatus = 0x02
)

// CommitLog records how each transaction ended, in memory and on disk.
//
// The file is append-only and headerless: a flat sequence of fixed-size
// records, read until EOF.
//
//	[8 bytes txID, little-endian][1 byte status]
//
// A later record for the same id replaces an earlier one, so logging a second
// outcome overwrites the first on replay (the file itself keeps both).
//
// Each write is followed by an fsync, because the commit mark must be as
// durable as the WAL record it follows. A CommitLog is not safe for concurrent
// use.
//
// TODO: buffer writes and flush on a timer; today every record is written and
// fsynced immediately, which is the durability choice, not an accident.
type CommitLog struct {
	file    *os.File
	records map[uint64]ClogStatus // status by transaction id; rebuilt by load
}

// OpenCommitLog opens or creates the commit log at path and replays it into
// memory. A file that does not exist yet starts empty.
func OpenCommitLog(path string) (*CommitLog, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("tx: open commit log %q: %w", path, err)
	}

	cl := &CommitLog{
		file:    f,
		records: make(map[uint64]ClogStatus),
	}

	if err := cl.load(); err != nil {
		f.Close() // best effort: the log is unusable either way
		return nil, fmt.Errorf("tx: load commit log %q: %w", path, err)
	}

	return cl, nil
}

// LogCommit records that a transaction committed, in memory and on disk. It
// syncs before returning, so a nil error means the mark is durable.
func (cl *CommitLog) LogCommit(txID uint64) error {
	cl.records[txID] = ClogStatusCommitted
	return cl.append(txID, ClogStatusCommitted)
}

// LogAbort records that a transaction aborted, in memory and on disk. It syncs
// before returning, so a nil error means the mark is durable.
func (cl *CommitLog) LogAbort(txID uint64) error {
	cl.records[txID] = ClogStatusAborted
	return cl.append(txID, ClogStatusAborted)
}

// IsCommitted reports whether the transaction is known to have committed. An
// unknown id is not committed.
func (cl *CommitLog) IsCommitted(txID uint64) bool {
	return cl.records[txID] == ClogStatusCommitted
}

// IsAborted reports whether the transaction is known to have aborted. An
// unknown id is not aborted.
func (cl *CommitLog) IsAborted(txID uint64) bool {
	return cl.records[txID] == ClogStatusAborted
}

// IsInProgress reports whether the transaction has no recorded outcome. An
// unknown id counts as in progress: it may be running, or it may have aborted
// before reaching the log.
func (cl *CommitLog) IsInProgress(txID uint64) bool {
	status, ok := cl.records[txID]
	if !ok {
		return true // unknown = in progress
	}
	return status == ClogStatusInProgress
}

// Close closes the log file. Reopen the same path to replay it.
func (cl *CommitLog) Close() error {
	return cl.file.Close()
}

// append writes one record at the end of the file and syncs it. Write-through
// is deliberate: the mark must not be lost if the process dies right after
// append returns.
func (cl *CommitLog) append(txID uint64, status ClogStatus) error {
	if _, err := cl.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("tx: seek commit log end: %w", err)
	}

	buf := make([]byte, clogRecordSize)
	binary.LittleEndian.PutUint64(buf[:8], txID)
	buf[8] = byte(status)

	if _, err := cl.file.Write(buf); err != nil {
		return fmt.Errorf("tx: write commit log record: %w", err)
	}
	if err := cl.file.Sync(); err != nil {
		return fmt.Errorf("tx: sync commit log: %w", err)
	}
	return nil
}

// load replays every record from disk into the in-memory map, starting at the
// beginning of the file. A later record for the same id replaces an earlier
// one.
func (cl *CommitLog) load() error {
	if _, err := cl.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("tx: seek commit log start: %w", err)
	}

	// read records until EOF
	for {
		recBuf := make([]byte, clogRecordSize)
		_, err := cl.file.Read(recBuf)
		if err != nil {
			break // EOF or error
		}
		txID := binary.LittleEndian.Uint64(recBuf[:8])
		status := ClogStatus(recBuf[8])
		cl.records[txID] = status
	}

	return nil
}

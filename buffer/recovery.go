package buffer

import (
	"errors"
	"fmt"
	"time"

	"database/tx"
	"database/wal"
)

// errTargetReached stops replay at the recovery target. It is private
// because the rule belongs to recovery, not to Iterate's contract, so no
// caller ever sees it.
var errTargetReached = errors.New("buffer: recovery target reached")

// Recover replays the WAL forward from the control file's REDO point,
// rebuilding pages that died dirty in memory, and returns the next
// transaction id to hand out. Single data file: insert payloads carry
// page ids, not file identity, so every record addresses dataPath (see
// plan follow-ups). Ends with FlushAll so recovered pages land on disk.
// Missing or corrupt control files refuse via ReadControl.
//
// Replay has two jobs, not one. A restored backup carries the CLOG file
// from backup time, so every commit made after the backup is missing from
// it, and every row those commits wrote reads back invisible
// (storage.TxRecord.Visible consults the CLOG on each read). The WAL holds
// the only surviving copy of those commits, and replay is the only thing
// that walks it after a restore, so replay re-marks the CLOG. PostgreSQL
// logs CLOG changes as their own WAL records; we log the commit instead
// and derive the mark from it here.
//
// A non-zero target stops replay at the first commit whose timestamp is
// past it. Later transactions keep their rows on the page but never get
// their mark, so visibility hides them. The zero time replays everything.
func (p *BufferPool) Recover(dataPath string, seg *wal.Segment, ctlPath string,
	clog *tx.CommitLog, target time.Time) (uint64, error) {
	_, redo, err := wal.ReadControl(ctlPath)
	if err != nil {
		return 0, err
	}
	var nextXid uint64
	err = seg.Iterate(redo, func(lsn wal.LSN, rec *wal.Record) error {
		// A record's Xid is an id already spent, so the counter must clear
		// it: no id is reused, because a row remembers its writer's id for
		// life. The checkpoint branch below needs no +1, because that
		// field stores the next free id instead of a spent one.
		if rec.Xid > 0 && rec.Xid+1 > nextXid {
			nextXid = rec.Xid + 1
		}
		switch rec.Type {
		case wal.RecordInsert:
			pageId, slot, bitmap, row, err := wal.ParseInsert(rec.Payload)
			if err != nil {
				return fmt.Errorf("buffer: bad insert at LSN %d: %w", lsn, err)
			}
			tag := BufferTag{Path: dataPath, PageId: pageId}
			page, err := p.Get(tag)
			if err != nil {
				return err
			}
			if wal.LSN(page.Header.LSN) >= lsn {
				// Page already holds this change (flushed before the crash).
				return p.Unpin(tag)
			}
			if !page.AddRecord(row, bitmap) {
				_ = p.Unpin(tag)
				return fmt.Errorf("buffer: replay overflows page %d", pageId)
			}
			if got := page.Header.RecordCount - 1; got != slot {
				_ = p.Unpin(tag)
				return fmt.Errorf("buffer: replay landed slot %d, log says %d", got, slot)
			}
			page.Header.LSN = uint64(lsn)
			if err := p.MarkDirty(tag); err != nil {
				_ = p.Unpin(tag)
				return err
			}
			return p.Unpin(tag)
		case wal.RecordCommit:
			if !target.IsZero() {
				ts, err := wal.ParseCommit(rec.Payload)
				if err != nil {
					return fmt.Errorf("buffer: bad commit at LSN %d: %w", lsn, err)
				}
				// Replay ends at the first commit whose timestamp exceeds
				// the target, so a commit landing exactly on the target
				// still applies. The counter above already cleared this
				// record's id: the transaction ran, so its id must not
				// come round again even though its effect stays hidden
				// behind the missing mark.
				if ts > target.UnixNano() {
					return errTargetReached
				}
			}
			return clog.LogCommit(rec.Xid)
		case wal.RecordCheckpoint:
			// The record carries the counter as of its own moment, and it
			// sits exactly at REDO, so replay meets it first. That makes it
			// the floor when the window holds no commits.
			_, xid, err := wal.ParseCheckpoint(rec.Payload)
			if err != nil {
				return fmt.Errorf("buffer: bad checkpoint at LSN %d: %w", lsn, err)
			}
			if xid > nextXid {
				nextXid = xid
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errTargetReached) {
		return 0, err
	}
	return nextXid, p.FlushAll()
}

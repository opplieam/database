// Package replication makes a standby follow a primary: bootstrap it
// from a completed backup, then ship the WAL written since.
package replication

import (
	"bytes"
	"time"

	"database/backup"
	"database/buffer"
	"database/tx"
	"database/wal"
)

// Status is the primary's view of one connected standby: how far the
// log was sent, how far the standby wrote it, and how far it replayed.
type Status struct {
	Sent    wal.LSN
	Flushed wal.LSN
	Applied wal.LSN
	State   string
}

// States a link passes through. Start-up and backup have no counterpart
// here: bootstrap is a separate step, and no live base backup streams.
const (
	StateCatchUp   = "catch-up"
	StateStreaming = "streaming"
)

// Standby is a cluster that follows a primary: its own copies of the
// files, plus the handles catch-up needs open.
type Standby struct {
	Cluster backup.Cluster
	seg     *wal.Segment
	pool    *buffer.BufferPool
	clog    *tx.CommitLog
}

// Bootstrap makes c into a standby holding the backup's state: the same
// files a restore produces, then replay to the backup's tip. A standby
// is a restore that keeps receiving more of the log, so this is the
// ordinary restore with no target and nothing left behind.
//
// The standby must be able to read the archive the label names, because
// that is where Restore takes the log from. A standby never archives;
// it follows the primary's archive, so c.Archives normally points there.
func Bootstrap(c backup.Cluster, backupDir string,
	mgr *tx.TxManager) (*Standby, error) {
	if err := backup.Restore(c, backupDir, time.Time{}, mgr); err != nil {
		return nil, err
	}

	seg, err := wal.Open(c.WAL)
	if err != nil {
		return nil, err
	}
	clog, err := tx.OpenCommitLog(c.Clog)
	if err != nil {
		seg.Close()
		return nil, err
	}
	return &Standby{
		Cluster: c,
		seg:     seg,
		pool:    buffer.NewBufferPool(4),
		clog:    clog,
	}, nil
}

// CatchUp ships the WAL the standby is missing, replays it, and reports
// the positions the primary would show. It is a call, not a loop: while
// the caller is doing something else the standby simply sits behind,
// which is the async contract.
//
// Replay always starts from REDO and the whole log is walked. That is
// deliberately dumb: pages carry the LSN they were last changed at, so
// records already replayed are skipped. Restartability comes from the
// same property, not from remembering where the last replay stopped.
func (s *Standby) CatchUp(primary *wal.Segment) (Status, error) {
	from := s.seg.End()

	var ship bytes.Buffer
	sent, err := primary.Send(&ship, from)
	if err != nil {
		return Status{}, err
	}

	flushed := from
	if ship.Len() > 0 {
		flushed, err = s.seg.Receive(ship.Bytes())
		if err != nil {
			return Status{}, err
		}
	}

	// Recover flushes the pages it touched before returning.
	_, applied, err := s.pool.Recover(s.Cluster.Data, s.seg,
		s.Cluster.Ctl, s.clog, time.Time{})
	if err != nil {
		return Status{}, err
	}

	state := StateStreaming
	if sent > from {
		state = StateCatchUp
	}
	return Status{
		Sent:    sent,
		Flushed: flushed,
		Applied: applied,
		State:   state,
	}, nil
}

// Close releases the standby's handles. The pool holds no bytes worth
// keeping: every catch-up already flushed.
func (s *Standby) Close() error {
	if err := s.pool.FlushAll(); err != nil {
		return err
	}
	if err := s.clog.Close(); err != nil {
		return err
	}
	return s.seg.Close()
}

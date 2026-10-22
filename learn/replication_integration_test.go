package learn

import (
	"os"
	"path/filepath"
	"testing"

	"database/backup"
	"database/buffer"
	"database/executors"
	"database/replication"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/suite"
)

type ReplicationSuite struct {
	suite.Suite
	mgr *tx.Manager
}

func (s *ReplicationSuite) SetupTest() {
	s.mgr = tx.NewManager()
}

// newPrimary starts a cluster on disk: one empty page, log and commit
// log open, pool wired through SetWAL. Cleanup closes and removes it.
func (s *ReplicationSuite) newPrimary(prefix string) (backup.Cluster,
	*buffer.BufferPool, *wal.Segment, *tx.CommitLog) {

	c := backup.Cluster{
		Data:     prefix + ".data",
		Clog:     prefix + ".clog",
		Ctl:      prefix + ".ctl",
		WAL:      prefix + ".wal",
		Archives: prefix + "_archive",
	}

	f, err := storage.Create(c.Data)
	s.Require().NoError(err)
	s.Require().NoError(f.AppendPage(storage.NewPage(0)))
	s.Require().NoError(f.Close())

	seg, err := wal.Open(c.WAL)
	s.Require().NoError(err)
	clog, err := tx.OpenCommitLog(c.Clog)
	s.Require().NoError(err)

	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)

	s.T().Cleanup(func() {
		clog.Close()
		seg.Close()
		os.Remove(c.Data)
		os.Remove(c.Clog)
		os.Remove(c.Ctl)
		os.Remove(c.WAL)
		os.RemoveAll(c.Archives)
	})
	return c, pool, seg, clog
}

// newStandby bootstraps a standby into its own directory from the given
// backup. The files keep the primary's names, since restore copies by
// basename. It reads the primary's archive, because a standby never
// archives: following is receiving, not keeping.
func (s *ReplicationSuite) newStandby(primary backup.Cluster, backupDir,
	prefix string) *replication.Standby {

	dir := prefix + "_dir"
	s.Require().NoError(os.MkdirAll(dir, 0755))

	c := backup.Cluster{
		Data:     filepath.Join(dir, filepath.Base(primary.Data)),
		Clog:     filepath.Join(dir, filepath.Base(primary.Clog)),
		Ctl:      filepath.Join(dir, filepath.Base(primary.Ctl)),
		WAL:      filepath.Join(dir, filepath.Base(primary.WAL)),
		Archives: primary.Archives,
	}
	st, err := replication.Bootstrap(c, backupDir, s.mgr)
	s.Require().NoError(err)

	s.T().Cleanup(func() {
		st.Close()
		os.RemoveAll(dir)
	})
	return st
}

// insertPooled writes one row through the pool in its own transaction:
// insert record, commit record, CLOG mark. The real write path.
func (s *ReplicationSuite) insertPooled(path string, pool *buffer.BufferPool,
	clog *tx.CommitLog, m storage.MovieRecord) {

	wtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: s.mgr.Snapshot(wtx),
	}
	ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
	_, err := ins.Next()
	s.Require().NoError(err)
	s.mgr.Commit(wtx)
}

// scanPooled reads every visible tuple through the given pool.
func (s *ReplicationSuite) scanPooled(path string, pool *buffer.BufferPool,
	clog *tx.CommitLog) []storage.Tuple {

	rtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       rtx,
		Clog:     clog,
		Snapshot: s.mgr.Snapshot(rtx),
	}
	scan, err := executors.NewPoolHeapScanMVCC(path, ctx, pool)
	s.Require().NoError(err)
	defer scan.Close()
	result, err := executors.Run(scan)
	s.Require().NoError(err)
	return result
}

// scanStandby reads the standby's files the way a client would: a fresh
// pool over its data file and a fresh handle on its commit log.
func (s *ReplicationSuite) scanStandby(st *replication.Standby) []storage.Tuple {
	clog, err := tx.OpenCommitLog(st.Cluster.Clog)
	s.Require().NoError(err)
	defer clog.Close()
	return s.scanPooled(st.Cluster.Data, buffer.NewBufferPool(4), clog)
}

// A standby starts as the backup and then follows: commits made after
// bootstrap are invisible until catch-up ships them, each catch-up
// moves only the tail, and a catch-up with nothing new is streaming
// rather than catching up.
func (s *ReplicationSuite) TestStandbyFollowsPrimary() {
	s.T().Logf("Step 1: Alice and Bob commit on the primary")
	c, pool, seg, clog := s.newPrimary("test_repl_follow")
	s.insertPooled(c.Data, pool, clog, storage.MovieRecord{MovieId: 1, Title: "Alice", Genres: "Genre"})
	s.insertPooled(c.Data, pool, clog, storage.MovieRecord{MovieId: 2, Title: "Bob", Genres: "Genre"})

	s.T().Logf("Step 2: Base backup")
	backupDir := "test_repl_follow_backup"
	defer os.RemoveAll(backupDir)
	s.Require().NoError(backup.Backup(c, backupDir, pool, seg, s.mgr.NextID()))

	s.T().Logf("Step 3: Bootstrap copies the backup and replays it to its tip")
	st := s.newStandby(c, backupDir, "test_repl_follow")
	rows := s.scanStandby(st)
	s.Require().Len(rows, 2)
	s.Equal("Alice", rows[0][1])
	s.Equal("Bob", rows[1][1])

	s.T().Logf("Step 4: Carol commits; the standby cannot see her yet")
	s.insertPooled(c.Data, pool, clog, storage.MovieRecord{MovieId: 3, Title: "Carol", Genres: "Genre"})
	rows = s.scanStandby(st)
	s.Require().Len(rows, 2, "the async gap is visible on disk")

	s.T().Logf("Step 5: Catch-up ships the tail and replays it")
	first, err := st.CatchUp(seg)
	s.Require().NoError(err)
	s.Equal(replication.StateCatchUp, first.State)
	s.Equal(seg.End(), first.Sent)
	s.GreaterOrEqual(first.Sent, first.Flushed)
	s.Equal(first.Flushed, first.Applied)

	rows = s.scanStandby(st)
	s.Require().Len(rows, 3)
	s.Equal("Carol", rows[2][1])

	s.T().Logf("Step 6: Dave commits; a second catch-up moves only the new tail")
	s.insertPooled(c.Data, pool, clog, storage.MovieRecord{MovieId: 4, Title: "Dave", Genres: "Genre"})
	second, err := st.CatchUp(seg)
	s.Require().NoError(err)
	s.Equal(replication.StateCatchUp, second.State)
	s.Equal(seg.End(), second.Sent)
	s.Greater(second.Sent, first.Sent)

	rows = s.scanStandby(st)
	s.Require().Len(rows, 4)
	s.Equal("Dave", rows[3][1])

	s.T().Logf("Step 7: With nothing new, the link is streaming and nothing changes")
	third, err := st.CatchUp(seg)
	s.Require().NoError(err)
	s.Equal(replication.StateStreaming, third.State)
	s.Equal(second.Sent, third.Sent)
	s.Equal(second.Flushed, third.Flushed)
	s.Equal(second.Applied, third.Applied)

	rows = s.scanStandby(st)
	s.Require().Len(rows, 4)
}

// Async replication is not lossy by itself; it is lossy inside the
// window. A commit the primary finished but never shipped is durable on
// the primary and simply absent on the standby, so losing the primary
// loses it. Catch up first and the same commit survives.
func (s *ReplicationSuite) TestLagIsDataLoss() {
	s.T().Logf("Step 1: Alice and Bob commit, backup, bootstrap a standby")
	ca, poolA, segA, clogA := s.newPrimary("test_repl_loss_a")
	s.insertPooled(ca.Data, poolA, clogA, storage.MovieRecord{MovieId: 1, Title: "Alice", Genres: "Genre"})
	s.insertPooled(ca.Data, poolA, clogA, storage.MovieRecord{MovieId: 2, Title: "Bob", Genres: "Genre"})
	backupDirA := "test_repl_loss_a_backup"
	defer os.RemoveAll(backupDirA)
	s.Require().NoError(backup.Backup(ca, backupDirA, poolA, segA, s.mgr.NextID()))
	stA := s.newStandby(ca, backupDirA, "test_repl_loss_a")

	s.T().Logf("Step 2: Carol commits on the primary")
	s.insertPooled(ca.Data, poolA, clogA, storage.MovieRecord{MovieId: 3, Title: "Carol", Genres: "Genre"})

	s.T().Logf("Step 3: The primary dies before any catch-up")
	s.Require().NoError(clogA.Close())
	s.Require().NoError(segA.Close())

	rows := s.scanStandby(stA)
	s.Require().Len(rows, 2, "Carol is committed and durable on the primary, absent here")
	s.Equal("Alice", rows[0][1])
	s.Equal("Bob", rows[1][1])

	s.T().Logf("Step 4: The contrast run: Alice and Bob commit, backup, bootstrap")
	cb, poolB, segB, clogB := s.newPrimary("test_repl_loss_b")
	s.insertPooled(cb.Data, poolB, clogB, storage.MovieRecord{MovieId: 1, Title: "Alice", Genres: "Genre"})
	s.insertPooled(cb.Data, poolB, clogB, storage.MovieRecord{MovieId: 2, Title: "Bob", Genres: "Genre"})
	backupDirB := "test_repl_loss_b_backup"
	defer os.RemoveAll(backupDirB)
	s.Require().NoError(backup.Backup(cb, backupDirB, poolB, segB, s.mgr.NextID()))
	stB := s.newStandby(cb, backupDirB, "test_repl_loss_b")

	s.T().Logf("Step 5: Carol commits and catch-up runs before the primary dies")
	s.insertPooled(cb.Data, poolB, clogB, storage.MovieRecord{MovieId: 3, Title: "Carol", Genres: "Genre"})
	_, err := stB.CatchUp(segB)
	s.Require().NoError(err)
	s.Require().NoError(clogB.Close())
	s.Require().NoError(segB.Close())

	rows = s.scanStandby(stB)
	s.Require().Len(rows, 3, "shipped before the failure, so it survives")
	s.Equal("Carol", rows[2][1])
}

func TestReplicationSuite(t *testing.T) {
	suite.Run(t, new(ReplicationSuite))
}

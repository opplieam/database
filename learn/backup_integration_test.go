package learn

import (
	"os"
	"testing"
	"time"

	"database/backup"
	"database/buffer"
	"database/executors"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/suite"
)

type BackupSuite struct {
	suite.Suite
	mgr *tx.TxManager
}

func (s *BackupSuite) SetupTest() {
	s.mgr = tx.NewTxManager()
}

// insertPooled writes one row through the pool in its own transaction:
// insert record, commit record, CLOG mark, timestamped by the commit
// path. The real write path, and each row's clock is its own.
func (s *BackupSuite) insertPooled(path string, pool *buffer.BufferPool,
	clog *tx.CommitLog, m storage.MovieRecord) {
	wtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     clog,
		Snapshot: wtx.ID(),
		XipList:  wtx.XipList(),
	}
	ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
	_, err := ins.Next()
	s.Require().NoError(err)
	s.mgr.Commit(wtx)
}

// scanPooled reads every visible tuple through the given pool.
func (s *BackupSuite) scanPooled(path string, pool *buffer.BufferPool,
	clog *tx.CommitLog) []storage.Tuple {
	rtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       rtx,
		Clog:     clog,
		Snapshot: rtx.ID(),
		XipList:  rtx.XipList(),
	}
	scan, err := executors.NewPoolHeapScanMVCC(path, ctx, pool)
	s.Require().NoError(err)
	defer scan.Close()
	result, err := executors.Run(scan)
	s.Require().NoError(err)
	return result
}

// The end-to-end chain of a point-in-time restore: rows committed
// before a backup survive it, a commit after it reaches the restored
// cluster through the newest archive, and restore to a target between
// two commits keeps the first commit while never replaying the second
// — not during the targeted replay, and not during any later
// untargeted recovery of the restored cluster either.
func (s *BackupSuite) TestPITRToTarget() {
	data := "test_pitr.data"
	walPath := "test_pitr.wal"
	ctl := "test_pitr.ctl"
	clogPath := "test_pitr.clog"
	backupDir := "test_pitr_backup"
	archiveDir := "test_pitr_archive"
	defer os.Remove(data)
	defer os.Remove(walPath)
	defer os.Remove(ctl)
	defer os.Remove(clogPath)
	defer os.RemoveAll(backupDir)
	defer os.RemoveAll(archiveDir)

	// The cluster exists before its first row: one empty page on
	// disk, which the pool fills rather than creates.
	f, err := storage.Create(data)
	s.Require().NoError(err)
	s.Require().NoError(f.AppendPage(storage.NewPage(0)))
	s.Require().NoError(f.Close())

	seg, err := wal.Open(walPath)
	s.Require().NoError(err)
	clog, err := tx.OpenCommitLog(clogPath)
	s.Require().NoError(err)

	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)

	c := backup.Cluster{
		Data: data, Clog: clogPath, Ctl: ctl,
		WAL: walPath, Archives: archiveDir,
	}

	s.T().Logf("Step 1: Alice and Bob commit before the backup")
	s.insertPooled(data, pool, clog, storage.MovieRecord{MovieId: 1, Title: "Alice", Genres: "Genre"})
	s.insertPooled(data, pool, clog, storage.MovieRecord{MovieId: 2, Title: "Bob", Genres: "Genre"})

	s.T().Logf("Step 2: Base backup: checkpoint, archive, copy files, label")
	s.Require().NoError(backup.Backup(c, backupDir, pool, seg, s.mgr.NextID()))

	s.T().Logf("Step 3: Carol commits after the backup")
	s.insertPooled(data, pool, clog, storage.MovieRecord{MovieId: 3, Title: "Carol", Genres: "Genre"})
	target := time.Now()

	s.T().Logf("Step 4: Dave commits past the target: the mistake")
	time.Sleep(5 * time.Millisecond)
	s.insertPooled(data, pool, clog, storage.MovieRecord{MovieId: 4, Title: "Dave", Genres: "Genre"})

	// The backup's archive ends at the backup tip, below where replay
	// must reach, so the log written since the backup is archived
	// here. One full-copy archive per backup today; segment rollover
	// would supply the later records differently.
	s.T().Logf("Step 5: Archive the log written since the backup, then restore between Carol and Dave")
	_, err = seg.Archive(archiveDir)
	s.Require().NoError(err)
	s.Require().NoError(backup.Restore(c, backupDir, target, s.mgr))
	clog.Close()
	s.Require().NoError(seg.Close())

	s.T().Logf("Step 6: The restored cluster holds Alice, Bob and Carol; Dave is invisible")
	restored := buffer.NewBufferPool(4)
	clogR, err := tx.OpenCommitLog(clogPath)
	s.Require().NoError(err)
	result := s.scanPooled(data, restored, clogR)
	s.Require().Equal(3, len(result))
	s.Equal("Alice", result[0][1])
	s.Equal("Bob", result[1][1])
	s.Equal("Carol", result[2][1])
	s.Equal(0, restored.PinnedCount())
	clogR.Close()

	s.T().Logf("Step 7: A later untargeted recovery of the restored cluster cannot resurrect Dave")
	seg2, err := wal.Open(walPath)
	s.Require().NoError(err)
	defer seg2.Close()
	pool2 := buffer.NewBufferPool(4)
	pool2.SetWAL(seg2)
	clog2, err := tx.OpenCommitLog(clogPath)
	s.Require().NoError(err)
	defer clog2.Close()
	nextXid, _, err := pool2.Recover(data, seg2, ctl, clog2, time.Time{})
	s.Require().NoError(err)
	s.mgr.SetNextID(nextXid)
	result = s.scanPooled(data, pool2, clog2)
	s.Require().Equal(3, len(result))
	s.Equal("Alice", result[0][1])
	s.Equal("Bob", result[1][1])
	s.Equal("Carol", result[2][1])
	s.Equal(0, pool2.PinnedCount())
}

func TestBackupSuite(t *testing.T) {
	suite.Run(t, new(BackupSuite))
}

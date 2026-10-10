package learn

import (
	"os"
	"testing"

	"database/buffer"
	"database/executors"
	"database/storage"
	"database/tx"
	"database/wal"

	"github.com/stretchr/testify/suite"
)

type WALSuite struct {
	suite.Suite
	clog     *tx.CommitLog
	clogFile string
	mgr      *tx.TxManager
}

func (s *WALSuite) SetupSuite() {
	s.clogFile = "test_wal_clog.data"
	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
}

func (s *WALSuite) SetupTest() {
	s.mgr = tx.NewTxManager()
}

func (s *WALSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.clogFile)
}

// writePooled inserts every record through the pool in one transaction.
// Each Next appends insert plus commit records and marks CLOG.
func (s *WALSuite) writePooled(path string, pool *buffer.BufferPool, movies []storage.MovieRecord) {
	wtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     s.clog,
		Snapshot: wtx.Id(),
		XipList:  wtx.XipList(),
	}
	for _, m := range movies {
		ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
		_, err := ins.Next()
		s.Require().NoError(err)
	}
	s.mgr.Commit(wtx)
}

// scanPooled reads every visible tuple through the given pool.
func (s *WALSuite) scanPooled(path string, pool *buffer.BufferPool) []storage.Tuple {
	rtx := s.mgr.Begin(tx.RepeatableRead)
	rctx := &executors.TransactionContext{
		Tx:       rtx,
		Clog:     s.clog,
		Snapshot: rtx.Id(),
		XipList:  rtx.XipList(),
	}
	scan, err := executors.NewPoolHeapScanMVCC(path, rctx, pool)
	s.Require().NoError(err)
	defer scan.Close()
	result, err := executors.Run(scan)
	s.Require().NoError(err)
	return result
}

// Crash between checkpoint and flush: rows before the checkpoint are on
// disk, the row after lives only in WAL. Recovery returns all three.
func (s *WALSuite) TestCrashRecovery() {
	path := "test_wal_crash.data"
	walPath := "test_wal_crash.wal"
	ctl := "test_wal_crash.ctl"
	defer os.Remove(path)
	defer os.Remove(walPath)
	defer os.Remove(ctl)

	seg, err := wal.Open(walPath)
	s.Require().NoError(err)
	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)

	s.T().Logf("Step 1: Insert 2 rows through the pool (log + CLOG, pages dirty)")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Genre"},
		{MovieId: 2, Title: "Jumanji", Genres: "Genre"},
	})

	s.T().Logf("Step 2: FlushAll lands both rows on disk")
	s.Require().NoError(pool.FlushAll())

	s.T().Logf("Step 3: Checkpoint bounds recovery from here")
	checkpoint, redo, err := wal.Checkpoint(pool, seg, ctl, s.mgr.NextID())
	s.Require().NoError(err)
	s.T().Logf("  - checkpoint %d, redo %d (equal: nothing appends mid-checkpoint)", checkpoint, redo)

	s.T().Logf("Step 4: Insert 1 more row, no flush. The crash takes the pool")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 3, Title: "Heat", Genres: "Genre"},
	})
	pool = nil // dropped dirty: memory state gone
	s.Require().NoError(seg.Close())

	s.T().Logf("Step 5: Restart reopens the log, fresh pool recovers from REDO")
	seg2, err := wal.Open(walPath)
	s.Require().NoError(err)
	defer seg2.Close()
	pool2 := buffer.NewBufferPool(4)
	s.Require().NoError(pool2.Recover(path, seg2, ctl))

	s.T().Logf("Step 6: All 3 rows visible exactly once through the fresh pool")
	result := s.scanPooled(path, pool2)
	s.Require().Equal(3, len(result))
	s.Equal("Toy Story", result[0][1])
	s.Equal("Jumanji", result[1][1])
	s.Equal("Heat", result[2][1])
	s.Equal(0, pool2.PinnedCount())
}

// A row flushed after REDO meets its own record during replay and must
// be skipped, not duplicated.
func (s *WALSuite) TestFlushedRecordSkipped() {
	path := "test_wal_skip.data"
	walPath := "test_wal_skip.wal"
	ctl := "test_wal_skip.ctl"
	defer os.Remove(path)
	defer os.Remove(walPath)
	defer os.Remove(ctl)

	seg, err := wal.Open(walPath)
	s.Require().NoError(err)
	pool := buffer.NewBufferPool(4)
	pool.SetWAL(seg)

	s.T().Logf("Step 1: Checkpoint on the empty db, REDO at the tip")
	_, _, err = wal.Checkpoint(pool, seg, ctl, s.mgr.NextID())
	s.Require().NoError(err)

	s.T().Logf("Step 2: Insert row A and flush (durable with its LSN)")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Genre"},
	})
	s.Require().NoError(pool.FlushAll())

	s.T().Logf("Step 3: Insert row B, no flush. The crash takes the pool")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 2, Title: "Jumanji", Genres: "Genre"},
	})
	pool = nil
	s.Require().NoError(seg.Close())

	s.T().Logf("Step 4: Recover replays from the old REDO: A skips (equal LSN), B applies")
	seg2, err := wal.Open(walPath)
	s.Require().NoError(err)
	defer seg2.Close()
	pool2 := buffer.NewBufferPool(4)
	s.Require().NoError(pool2.Recover(path, seg2, ctl))

	s.T().Logf("Step 5: Both rows present, A exactly once")
	result := s.scanPooled(path, pool2)
	s.Require().Equal(2, len(result))
	s.Equal("Toy Story", result[0][1])
	s.Equal("Jumanji", result[1][1])
}

func TestWALSuite(t *testing.T) {
	suite.Run(t, new(WALSuite))
}

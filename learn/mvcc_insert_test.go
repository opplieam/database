package learn

import (
	"os"
	"testing"

	"database/executors"
	"database/storage"
	"database/tx"

	"github.com/stretchr/testify/suite"
)

type MvccInsertSuite struct {
	suite.Suite
	clog     *tx.CommitLog
	clogFile string
	mgr      *tx.TxManager
}

func (s *MvccInsertSuite) SetupSuite() {
	s.clogFile = "test_mvcc_insert_clog.data"
	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
}

func (s *MvccInsertSuite) SetupTest() {
	s.mgr = tx.NewTxManager()
}

func (s *MvccInsertSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.clogFile)
}

func (s *MvccInsertSuite) TestMVCCInsertAndScan() {
	s.T().Logf("Step 1: Begin transaction")
	tx1 := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       tx1,
		Clog:     s.clog,
		Snapshot: tx1.Id(),
		XipList:  tx1.XipList(),
	}

	s.T().Logf("Step 2: Insert record with MVCC")
	insert := executors.NewInsert("test_mvcc_insert.data", storage.MovieRecord{
		MovieId: 1,
		Title:   "Toy Story",
		Genres:  "Animation",
	}, 0, ctx)
	_, err := insert.Next()
	s.Require().NoError(err)
	defer os.Remove("test_mvcc_insert.data")
	defer os.Remove("test_mvcc_insert.data.fsm")

	s.T().Logf("Step 3: Commit transaction")
	s.mgr.Commit(tx1)

	s.T().Logf("Step 4: Begin new transaction to scan")
	tx2 := s.mgr.Begin(tx.RepeatableRead)
	ctx2 := &executors.TransactionContext{
		Tx:       tx2,
		Clog:     s.clog,
		Snapshot: tx2.Id(),
		XipList:  tx2.XipList(),
	}

	s.T().Logf("Step 5: Scan and verify record is visible")
	scan, err := executors.NewHeapFileScan("test_mvcc_insert.data", ctx2)
	s.Require().NoError(err)
	defer scan.Close()

	result, err := executors.Run(scan)
	s.Require().NoError(err)

	s.Equal(1, len(result), "should see 1 committed record")
	s.Equal("Toy Story", result[0][1], "title should be Toy Story")
}

func (s *MvccInsertSuite) TestMVCCInsertUncommitted() {
	s.T().Logf("Step 1: Begin transaction (NOT committed)")
	tx1 := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       tx1,
		Clog:     s.clog,
		Snapshot: tx1.Id(),
		XipList:  tx1.XipList(),
	}

	s.T().Logf("Step 2: Insert record WITHOUT commit")
	insert := executors.NewInsert("test_mvcc_uncommitted.data", storage.MovieRecord{
		MovieId: 1,
		Title:   "Toy Story",
		Genres:  "Animation",
	}, 0, ctx)
	_, err := insert.Next()
	s.Require().NoError(err)
	defer os.Remove("test_mvcc_uncommitted.data")
	defer os.Remove("test_mvcc_uncommitted.data.fsm")

	s.T().Logf("Step 3: Scan with NEW transaction")
	tx2 := s.mgr.Begin(tx.RepeatableRead)
	ctx2 := &executors.TransactionContext{
		Tx:       tx2,
		Clog:     s.clog,
		Snapshot: tx2.Id(),
		XipList:  tx2.XipList(),
	}

	scan, err := executors.NewHeapFileScan("test_mvcc_uncommitted.data", ctx2)
	s.Require().NoError(err)
	defer scan.Close()

	result, err := executors.Run(scan)
	s.Require().NoError(err)

	s.Equal(0, len(result), "should NOT see uncommitted record")
}

func (s *MvccInsertSuite) TestMVCCInsertMultipleAndScan() {
	s.T().Logf("Step 1: Insert 3 records with tx1")
	tx1 := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       tx1,
		Clog:     s.clog,
		Snapshot: tx1.Id(),
		XipList:  tx1.XipList(),
	}

	movies := []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Animation"},
		{MovieId: 2, Title: "Jumanji", Genres: "Adventure"},
		{MovieId: 3, Title: "Grumpy Old Men", Genres: "Comedy"},
	}

	for _, m := range movies {
		insert := executors.NewInsert("test_mvcc_multi.data", m, 0, ctx)
		_, err := insert.Next()
		s.Require().NoError(err)
	}
	defer os.Remove("test_mvcc_multi.data")
	defer os.Remove("test_mvcc_multi.data.fsm")

	s.T().Logf("Step 2: Commit tx1")
	s.mgr.Commit(tx1)

	s.T().Logf("Step 3: Insert 2 more records with tx2")
	tx2 := s.mgr.Begin(tx.RepeatableRead)
	ctx2 := &executors.TransactionContext{
		Tx:       tx2,
		Clog:     s.clog,
		Snapshot: tx2.Id(),
		XipList:  tx2.XipList(),
	}

	moreMovies := []storage.MovieRecord{
		{MovieId: 4, Title: "Waiting to Exhale", Genres: "Comedy"},
		{MovieId: 5, Title: "Heat", Genres: "Crime"},
	}

	for _, m := range moreMovies {
		insert := executors.NewInsert("test_mvcc_multi.data", m, 0, ctx2)
		_, err := insert.Next()
		s.Require().NoError(err)
	}

	s.T().Logf("Step 4: Commit tx2")
	s.mgr.Commit(tx2)

	s.T().Logf("Step 5: Scan with tx3 - should see all 5 records")
	tx3 := s.mgr.Begin(tx.RepeatableRead)
	ctx3 := &executors.TransactionContext{
		Tx:       tx3,
		Clog:     s.clog,
		Snapshot: tx3.Id(),
		XipList:  tx3.XipList(),
	}

	scan, err := executors.NewHeapFileScan("test_mvcc_multi.data", ctx3)
	s.Require().NoError(err)
	defer scan.Close()

	result, err := executors.Run(scan)
	s.Require().NoError(err)

	s.Equal(5, len(result), "should see all 5 committed records")
}

func TestMvccInsertSuite(t *testing.T) {
	suite.Run(t, new(MvccInsertSuite))
}

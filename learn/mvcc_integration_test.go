package learn

import (
	"os"
	"testing"

	"database/storage"
	"database/tx"

	"github.com/stretchr/testify/suite"
)

type MvccSuite struct {
	suite.Suite
	clog     *tx.CommitLog
	clogFile string
	mgr      *tx.TxManager
}

func (s *MvccSuite) SetupSuite() {
	s.clogFile = "test_mvcc_clog.data"
	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
	s.mgr = tx.NewTxManager()
}

func (s *MvccSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.clogFile)
}

func (s *MvccSuite) TestTxLifecycle() {
	s.T().Logf("Step 1: Create TxManager")
	mgr := tx.NewTxManager()

	s.T().Logf("Step 2: Begin two transactions")
	tx1 := mgr.Begin(tx.RepeatableRead)
	tx2 := mgr.Begin(tx.ReadCommitted)
	s.Equal(uint64(1), tx1.Id())
	s.Equal(uint64(2), tx2.Id())

	s.T().Logf("Step 3: Both transactions are active")
	s.True(mgr.IsActive(tx1.Id()))
	s.True(mgr.IsActive(tx2.Id()))

	s.T().Logf("Step 4: Commit tx1")
	mgr.Commit(tx1)
	s.False(mgr.IsActive(tx1.Id()))
	s.Equal(tx.TxStatusCommitted, tx1.Status())

	s.T().Logf("Step 5: Rollback tx2")
	mgr.Rollback(tx2)
	s.False(mgr.IsActive(tx2.Id()))
	s.Equal(tx.TxStatusAborted, tx2.Status())
}

func (s *MvccSuite) TestSnapshotIsolationRepeatableRead() {
	s.T().Logf("Setup: Create initial record (tx=0 commits)")
	rec := storage.InsertTxRecord(0, 0, storage.Tuple{"Toy Story"})
	s.clog.LogCommit(0)

	s.T().Logf("Step 1: tx2 starts FIRST (id=1, snapshot=1)")
	tx2 := s.mgr.Begin(tx.RepeatableRead)

	s.T().Logf("Step 2: tx1 starts AFTER (id=2)")
	tx1 := s.mgr.Begin(tx.RepeatableRead)

	s.T().Logf("Step 3: Both read initial data")
	s.True(rec.Visible(tx1.Id(), s.clog), "tx1 sees initial record")
	s.True(rec.Visible(tx2.Id(), s.clog), "tx2 sees initial record")

	s.T().Logf("Step 4: tx1 updates and commits")
	oldRec, newRec := storage.UpdateRecord(rec, tx1.Id(), 0, storage.Tuple{"Toy Story (Remastered)"})
	s.clog.LogCommit(tx1.Id())

	s.T().Logf("Step 5: tx2 reads again (snapshot=1, FIXED)")
	s.T().Logf("  - Old version: TxMax=2 > snapshot=1 → delete happened AFTER snapshot → VISIBLE")
	s.True(oldRec.Visible(tx2.Id(), s.clog), "tx2 sees OLD version (delete after snapshot)")

	s.T().Logf("  - New version: TxMin=2 > snapshot=1 → created AFTER snapshot → NOT visible")
	s.False(newRec.Visible(tx2.Id(), s.clog), "tx2 does NOT see NEW version (created after snapshot)")

	s.T().Logf("Key insight: Repeatable Read - tx2 still sees 'Toy Story' (old version)")
}

func (s *MvccSuite) TestSnapshotIsolationReadCommitted() {
	s.T().Logf("Setup: Create initial record (tx=0 commits)")
	rec := storage.InsertTxRecord(0, 0, storage.Tuple{"Toy Story"})
	s.clog.LogCommit(0)

	s.T().Logf("Step 1: tx2 starts FIRST (id=1)")
	tx2 := s.mgr.Begin(tx.ReadCommitted)

	s.T().Logf("Step 2: tx1 starts AFTER (id=2)")
	tx1 := s.mgr.Begin(tx.ReadCommitted)

	s.T().Logf("Step 3: tx2 reads initial data (first statement)")
	snapshot1 := tx2.NewStatement()
	s.True(rec.Visible(snapshot1, s.clog), "tx2 sees initial record")

	s.T().Logf("Step 4: tx1 updates and commits")
	oldRec, newRec := storage.UpdateRecord(rec, tx1.Id(), 0, storage.Tuple{"Toy Story (Remastered)"})
	s.clog.LogCommit(tx1.Id())

	s.T().Logf("Step 5: tx2 reads again (NEW snapshot)")
	snapshot2 := tx2.NewStatement()
	s.T().Logf("  - Old version: TxMax=2, committed, 2 <= %d → delete BEFORE new snapshot → NOT visible", snapshot2)
	s.False(oldRec.Visible(snapshot2, s.clog), "tx2 does NOT see OLD version (delete before new snapshot)")

	s.T().Logf("  - New version: TxMin=2, committed, 2 <= %d → VISIBLE", snapshot2)
	s.True(newRec.Visible(snapshot2, s.clog), "tx2 sees NEW version (committed before new snapshot)")

	s.T().Logf("Key insight: Read Committed - tx2 now sees 'Toy Story (Remastered)' (new version)")
}

func TestMvccSuite(t *testing.T) {
	suite.Run(t, new(MvccSuite))
}

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
	mgr      *tx.Manager
}

func (s *MvccSuite) SetupSuite() {
	s.clogFile = "test_mvcc_clog.data"
	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
}

func (s *MvccSuite) SetupTest() {
	s.mgr = tx.NewManager()
}

func (s *MvccSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.clogFile)
}

func (s *MvccSuite) TestTxLifecycle() {
	s.T().Logf("Step 1: Create transaction manager")
	mgr := tx.NewManager()

	s.T().Logf("Step 2: Begin two transactions")
	tx1 := mgr.Begin(tx.RepeatableRead)
	tx2 := mgr.Begin(tx.ReadCommitted)
	s.Equal(uint64(1), tx1.ID())
	s.Equal(uint64(2), tx2.ID())

	s.T().Logf("Step 3: Both transactions are active")
	s.True(mgr.IsActive(tx1.ID()))
	s.True(mgr.IsActive(tx2.ID()))

	s.T().Logf("Step 4: Commit tx1")
	mgr.Commit(tx1)
	s.False(mgr.IsActive(tx1.ID()))
	s.Equal(tx.StatusCommitted, tx1.Status())

	s.T().Logf("Step 5: Rollback tx2")
	mgr.Rollback(tx2)
	s.False(mgr.IsActive(tx2.ID()))
	s.Equal(tx.StatusAborted, tx2.Status())
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
	s.T().Logf("  - tx1 xipList: %v (tx1 started after tx2, so tx2 is NOT in tx1's xipList)", s.mgr.Snapshot(tx1).Xip)
	s.T().Logf("  - tx2 xipList: %v (tx1 was in-progress when tx2 started)", s.mgr.Snapshot(tx2).Xip)
	s.True(rec.Visible(s.mgr.Snapshot(tx1), s.clog), "tx1 sees initial record")
	s.True(rec.Visible(s.mgr.Snapshot(tx2), s.clog), "tx2 sees initial record")

	s.T().Logf("Step 4: tx1 updates and commits")
	oldRec, newRec := storage.UpdateRecord(rec, tx1.ID(), 0, storage.Tuple{"Toy Story (Remastered)"})
	s.mgr.Commit(tx1)
	s.clog.LogCommit(tx1.ID())

	s.T().Logf("Step 5: tx2 reads again (snapshot=1, FIXED)")
	s.T().Logf("  - tx2 xipList: %v (tx1 is still in xipList because xipList is fixed at snapshot time)", s.mgr.Snapshot(tx2).Xip)
	s.T().Logf("  - Old version: TxMax=2 > snapshot=1 → delete happened AFTER snapshot → VISIBLE")
	s.True(oldRec.Visible(s.mgr.Snapshot(tx2), s.clog), "tx2 sees OLD version (delete after snapshot)")

	s.T().Logf("  - New version: TxMin=2 > snapshot=1 → created AFTER snapshot → NOT visible")
	s.False(newRec.Visible(s.mgr.Snapshot(tx2), s.clog), "tx2 does NOT see NEW version (created after snapshot)")

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
	snapshot1 := s.mgr.Snapshot(tx2)
	s.T().Logf("  - tx2 xipList after Snapshot: %v (refreshed, tx1 is in-progress)", snapshot1.Xip)
	s.True(rec.Visible(snapshot1, s.clog), "tx2 sees initial record")

	s.T().Logf("Step 4: tx1 updates and commits")
	oldRec, newRec := storage.UpdateRecord(rec, tx1.ID(), 0, storage.Tuple{"Toy Story (Remastered)"})
	s.mgr.Commit(tx1)
	s.clog.LogCommit(tx1.ID())

	s.T().Logf("Step 5: tx2 reads again (new snapshot: cutoff unchanged, xip refreshed)")
	snapshot2 := s.mgr.Snapshot(tx2)
	s.T().Logf("  - tx2 xipList after Snapshot: %v (refreshed, tx1 is NO LONGER in xipList because it committed)", snapshot2.Xip)
	s.T().Logf("  - Old version: TxMax=2, committed, 2 < %d → delete BEFORE new snapshot → NOT visible", snapshot2.Cutoff)
	s.False(oldRec.Visible(snapshot2, s.clog), "tx2 does NOT see OLD version (delete before new snapshot)")

	s.T().Logf("  - New version: TxMin=2, committed, 2 < %d → VISIBLE", snapshot2.Cutoff)
	s.True(newRec.Visible(snapshot2, s.clog), "tx2 sees NEW version (committed before new snapshot)")

	s.T().Logf("Key insight: Read Committed - tx2 now sees 'Toy Story (Remastered)' (new version)")
}

func TestMvccSuite(t *testing.T) {
	suite.Run(t, new(MvccSuite))
}

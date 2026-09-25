package learn

import (
	"os"
	"testing"

	"database/btree"
	"database/storage"
	"database/tx"
	"database/vacuum"

	"github.com/stretchr/testify/suite"
)

type VacuumSuite struct {
	suite.Suite
	heapFile string
	fsmFile  string
	clogFile string
	clog     *tx.CommitLog
}

func (s *VacuumSuite) SetupSuite() {
	s.heapFile = "test_vacuum_heap.data"
	s.fsmFile = "test_vacuum_fsm.fsm"
	s.clogFile = "test_vacuum_clog.data"

	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
}

func (s *VacuumSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.heapFile)
	os.Remove(s.fsmFile)
	os.Remove(s.clogFile)
}

// TestFullVacuumFlow demonstrates all VACUUM phases working together.
func (s *VacuumSuite) TestFullVacuumFlow() {
	s.T().Logf("=== VACUUM Integration Test ===")

	// Step 1: Create heap file with 5 tuples
	s.T().Logf("Step 1: Create heap file with 5 tuples")
	s.T().Logf("  - Tuple 0: TxMin=1, TxMax=0 (alive, old - needs freeze)")
	s.T().Logf("  - Tuple 1: TxMin=2, TxMax=0 (alive, recent)")
	s.T().Logf("  - Tuple 2: TxMin=3, TxMax=4 (dead - deleted by tx 4)")
	s.T().Logf("  - Tuple 3: TxMin=5, TxMax=6 (dead - deleted by tx 6)")
	s.T().Logf("  - Tuple 4: TxMin=7, TxMax=0 (alive, recent)")

	file, err := storage.Create(s.heapFile)
	s.Require().NoError(err)

	page := storage.NewPage(0)
	records := []storage.TxRecord{
		{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"Movie 1"}},
		{TxMin: 2, TxMax: 0, CID: 0, Data: storage.Tuple{"Movie 2"}},
		{TxMin: 3, TxMax: 4, CID: 0, Data: storage.Tuple{"Movie 3"}},
		{TxMin: 5, TxMax: 6, CID: 0, Data: storage.Tuple{"Movie 4"}},
		{TxMin: 7, TxMax: 0, CID: 0, Data: storage.Tuple{"Movie 5"}},
	}
	for _, rec := range records {
		encoded := storage.EncodeTxRecord(rec)
		page.AddRecord(encoded, 0)
	}
	err = file.AppendPage(page)
	s.Require().NoError(err)
	file.Close()

	// Step 2: Create commit log
	s.T().Logf("Step 2: Create commit log")
	s.T().Logf("  - tx 4 committed (deletion of tuple 2)")
	s.T().Logf("  - tx 6 committed (deletion of tuple 3)")
	err = s.clog.LogCommit(4)
	s.Require().NoError(err)
	err = s.clog.LogCommit(6)
	s.Require().NoError(err)

	// Step 3: Create B+ tree index
	s.T().Logf("Step 3: Create B+ tree index")
	s.T().Logf("  - 100 → TID(0,0)")
	s.T().Logf("  - 200 → TID(0,1)")
	s.T().Logf("  - 300 → TID(0,2) [dead]")
	s.T().Logf("  - 400 → TID(0,3) [dead]")
	s.T().Logf("  - 500 → TID(0,4)")
	bt := btree.NewBTree()
	bt.Insert(100, storage.TID{PageId: 0, SlotId: 0})
	bt.Insert(200, storage.TID{PageId: 0, SlotId: 1})
	bt.Insert(300, storage.TID{PageId: 0, SlotId: 2})
	bt.Insert(400, storage.TID{PageId: 0, SlotId: 3})
	bt.Insert(500, storage.TID{PageId: 0, SlotId: 4})

	// Step 4: Create FSM
	s.T().Logf("Step 4: Create FSM")
	s.T().Logf("  - Initial free space: 4000 bytes")
	fsm, err := storage.CreateFSM(s.fsmFile)
	s.Require().NoError(err)
	fsm.AddPage(4000)
	fsm.Close()

	// Step 5: Run VACUUM
	s.T().Logf("Step 5: Run VACUUM")
	s.T().Logf("  - currentTxID=10, freezeMaxAge=5")
	s.T().Logf("  - Cutoff = 10 - 5 = 5 (freeze if TxMin < 5)")
	stats, err := vacuum.Vacuum(s.heapFile, s.clog, bt, s.fsmFile,
		map[uint64]bool{}, 10, 5)
	s.Require().NoError(err)

	// Step 6: Verify results
	s.T().Logf("Step 6: Verify results")
	s.T().Logf("  - Dead tuples found: %d (expected 2)", stats.DeadTuples)
	s.T().Logf("  - Tuples frozen: %d (expected 2 - TxMin=1 and TxMin=3)", stats.FrozenTuples)
	s.T().Logf("  - Index entries removed: %d (expected 2)", stats.IndexEntries)
	s.Equal(2, stats.DeadTuples)
	s.Equal(2, stats.FrozenTuples)
	s.Equal(2, stats.IndexEntries)

	// Step 7: Verify index cleaned
	s.T().Logf("Step 7: Verify index cleaned")
	_, found := bt.Search(300)
	s.False(found, "index entry 300 should be removed")
	_, found = bt.Search(400)
	s.False(found, "index entry 400 should be removed")
	_, found = bt.Search(100)
	s.True(found, "index entry 100 should remain")
	_, found = bt.Search(200)
	s.True(found, "index entry 200 should remain")
	_, found = bt.Search(500)
	s.True(found, "index entry 500 should remain")

	// Step 8: Verify FSM updated
	s.T().Logf("Step 8: Verify FSM updated")
	fsm, err = storage.OpenFSM(s.fsmFile)
	s.Require().NoError(err)
	freeSpace := fsm.Get(0)
	s.T().Logf("  - Free space after VACUUM: %d bytes", freeSpace)
	s.T().Logf("  - FSM is tracking page 0 (free space > 0)")
	s.Greater(int(freeSpace), 0)
	fsm.Close()

	s.T().Logf("=== VACUUM Integration Test Complete ===")
}

// TestVacuumNoIndex demonstrates VACUUM without an index.
func (s *VacuumSuite) TestVacuumNoIndex() {
	s.T().Logf("=== VACUUM without Index ===")

	// Create heap file with 2 dead tuples
	s.T().Logf("Step 1: Create heap file with 2 dead tuples")
	file, err := storage.Create(s.heapFile)
	s.Require().NoError(err)

	page := storage.NewPage(0)
	records := []storage.TxRecord{
		{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"Alive"}},
		{TxMin: 2, TxMax: 3, CID: 0, Data: storage.Tuple{"Dead 1"}},
		{TxMin: 4, TxMax: 5, CID: 0, Data: storage.Tuple{"Dead 2"}},
	}
	for _, rec := range records {
		encoded := storage.EncodeTxRecord(rec)
		page.AddRecord(encoded, 0)
	}
	err = file.AppendPage(page)
	s.Require().NoError(err)
	file.Close()

	// Create commit log
	err = s.clog.LogCommit(3)
	s.Require().NoError(err)
	err = s.clog.LogCommit(5)
	s.Require().NoError(err)

	// Run VACUUM without index
	s.T().Logf("Step 2: Run VACUUM (bt=nil)")
	stats, err := vacuum.Vacuum(s.heapFile, s.clog, nil, "",
		map[uint64]bool{}, 10, 5)
	s.Require().NoError(err)

	s.T().Logf("Step 3: Verify results")
	s.T().Logf("  - Dead tuples: %d (expected 2)", stats.DeadTuples)
	s.T().Logf("  - Index entries removed: %d (expected 0, no index)", stats.IndexEntries)
	s.Equal(2, stats.DeadTuples)
	s.Equal(0, stats.IndexEntries)

	s.T().Logf("=== VACUUM without Index Complete ===")
}

func TestVacuumSuite(t *testing.T) {
	suite.Run(t, new(VacuumSuite))
}

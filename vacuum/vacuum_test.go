package vacuum

import (
	"os"
	"testing"

	"database/storage"
	"database/tx"
	"database/btree"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test helper: create a heap file with TxRecords
func createTestHeapFile(t *testing.T, filename string, records []storage.TxRecord) {
	t.Helper()

	// Create file
	file, err := storage.Create(filename)
	require.NoError(t, err)
	defer file.Close()

	// Add each record to a new page
	for i, rec := range records {
		encoded := storage.EncodeTxRecord(rec)
		page := storage.NewPage(uint32(i))
		page.AddRecord(encoded, 0)
		require.NoError(t, file.AppendPage(page))
	}
}

// Test helper: create a heap file with all TxRecords on the same page
func createTestHeapFileSinglePage(t *testing.T, filename string, records []storage.TxRecord) {
	t.Helper()

	// Create file
	file, err := storage.Create(filename)
	require.NoError(t, err)
	defer file.Close()

	// Create a single page with all records
	page := storage.NewPage(0)
	for _, rec := range records {
		encoded := storage.EncodeTxRecord(rec)
		page.AddRecord(encoded, 0)
	}
	require.NoError(t, file.AppendPage(page))
}

// Test helper: create a CommitLog with committed transactions
func createTestClog(t *testing.T, filename string, committed []uint64) *tx.CommitLog {
	t.Helper()

	clog, err := tx.OpenCommitLog(filename)
	require.NoError(t, err)

	for _, txId := range committed {
		require.NoError(t, clog.LogCommit(txId))
	}

	return clog
}

// =============================================================================
// Tests for isDead (table-driven)
// =============================================================================

func TestIsDead(t *testing.T) {
	tests := []struct {
		name      string
		txRecord  storage.TxRecord
		committed []uint64
		xipList   map[uint64]bool
		expected  bool
	}{
		{
			name:      "TxMaxZero_Alive",
			txRecord:  storage.TxRecord{TxMin: 1, TxMax: 0, CID: 0},
			committed: []uint64{1},
			xipList:   map[uint64]bool{},
			expected:  false,
		},
		{
			name:      "TxMaxCommitted_NotInXipList",
			txRecord:  storage.TxRecord{TxMin: 1, TxMax: 2, CID: 0},
			committed: []uint64{1, 2},
			xipList:   map[uint64]bool{},
			expected:  true,
		},
		{
			name:      "TxMaxInXipList",
			txRecord:  storage.TxRecord{TxMin: 1, TxMax: 2, CID: 0},
			committed: []uint64{1, 2},
			xipList:   map[uint64]bool{2: true},
			expected:  false,
		},
		{
			name:      "TxMaxNotCommitted",
			txRecord:  storage.TxRecord{TxMin: 1, TxMax: 2, CID: 0},
			committed: []uint64{1},
			xipList:   map[uint64]bool{},
			expected:  false,
		},
		{
			name:      "ComplexScenario_Tx5DeletedByTx5",
			txRecord:  storage.TxRecord{TxMin: 1, TxMax: 5, CID: 0},
			committed: []uint64{1, 5},
			xipList:   map[uint64]bool{3: true},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clogFile := "test_clog_" + tt.name + ".db"
			clog := createTestClog(t, clogFile, tt.committed)
			defer os.Remove(clogFile)
			defer clog.Close()

			result := isDead(tt.txRecord, clog, tt.xipList)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Tests for shouldFreeze (table-driven)
// =============================================================================

func TestShouldFreeze(t *testing.T) {
	tests := []struct {
		name         string
		txMin        uint64
		currentTxID  uint64
		freezeMaxAge uint64
		expected     bool
	}{
		// Normal cases (no wraparound)
		{
			name:         "Normal_OldTuple",
			txMin:        30,
			currentTxID:  100,
			freezeMaxAge: 50,
			expected:     true,
		},
		{
			name:         "Normal_NewTuple",
			txMin:        70,
			currentTxID:  100,
			freezeMaxAge: 50,
			expected:     false,
		},
		{
			name:         "Normal_ExactlyCutoff",
			txMin:        50,
			currentTxID:  100,
			freezeMaxAge: 50,
			expected:     false,
		},
		{
			name:         "Normal_ExactlyCutoffMinusOne",
			txMin:        49,
			currentTxID:  100,
			freezeMaxAge: 50,
			expected:     true,
		},

		// Wraparound cases (currentTxID < freezeMaxAge)
		{
			name:         "Wraparound_RecentTuple_TxMin0",
			txMin:        0,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     false,
		},
		{
			name:         "Wraparound_RecentTuple_TxMin1",
			txMin:        1,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     false,
		},
		{
			name:         "Wraparound_CurrentTx",
			txMin:        2,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     false,
		},
		{
			name:         "Wraparound_OldTuple_TxMin3",
			txMin:        3,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     true,
		},
		{
			name:         "Wraparound_OldTuple_TxMin5",
			txMin:        5,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     true,
		},
		{
			name:         "Wraparound_OldTuple_TxMin9",
			txMin:        9,
			currentTxID:  2,
			freezeMaxAge: 3,
			expected:     true,
		},

		// Edge cases
		{
			name:         "ZeroFreezeMaxAge",
			txMin:        50,
			currentTxID:  100,
			freezeMaxAge: 0,
			expected:     true,
		},
		{
			name:         "LargeNumbers_OldTuple",
			txMin:        40000000,
			currentTxID:  150000000,
			freezeMaxAge: 100000000,
			expected:     true,
		},
		{
			name:         "LargeNumbers_NewTuple",
			txMin:        60000000,
			currentTxID:  150000000,
			freezeMaxAge: 100000000,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldFreeze(tt.txMin, tt.currentTxID, tt.freezeMaxAge)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Tests for ScanHeap (table-driven)
// =============================================================================

func TestScanHeap(t *testing.T) {
	tests := []struct {
		name            string
		records         []storage.TxRecord
		committed       []uint64
		xipList         map[uint64]bool
		currentTxID     uint64
		freezeMaxAge    uint64
		expectedDead    int
		expectedFrozen  int
	}{
		{
			name:         "EmptyFile",
			records:      []storage.TxRecord{},
			committed:    []uint64{},
			xipList:      map[uint64]bool{},
			currentTxID:  100,
			freezeMaxAge: 50,
			expectedDead: 0,
			expectedFrozen: 0,
		},
		{
			name: "NoDeadTuples_AllFrozen",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0},
				{TxMin: 2, TxMax: 0, CID: 0},
				{TxMin: 3, TxMax: 0, CID: 0},
			},
			committed:    []uint64{1, 2, 3},
			xipList:      map[uint64]bool{},
			currentTxID:  100,
			freezeMaxAge: 10,
			expectedDead: 0,
			expectedFrozen: 3,
		},
		{
			name: "WithDeadTuples",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0},
				{TxMin: 2, TxMax: 3, CID: 0},
				{TxMin: 4, TxMax: 0, CID: 0},
				{TxMin: 5, TxMax: 6, CID: 0},
			},
			committed:    []uint64{1, 2, 3, 4, 5, 6},
			xipList:      map[uint64]bool{},
			currentTxID:  100,
			freezeMaxAge: 10,
			expectedDead: 2,
			expectedFrozen: 2,
		},
		{
			name: "DeadTupleNotFrozen",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 2, CID: 0},
			},
			committed:    []uint64{1, 2},
			xipList:      map[uint64]bool{},
			currentTxID:  100,
			freezeMaxAge: 50,
			expectedDead: 1,
			expectedFrozen: 0,
		},
		{
			name: "WithXipList_DeleteInProgress",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 2, CID: 0},
			},
			committed:    []uint64{1, 2},
			xipList:      map[uint64]bool{2: true},
			currentTxID:  100,
			freezeMaxAge: 50,
			expectedDead: 0,
			expectedFrozen: 1,
		},
		{
			name: "ComplexScenario",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0},
				{TxMin: 2, TxMax: 3, CID: 0},
				{TxMin: 4, TxMax: 0, CID: 0},
				{TxMin: 5, TxMax: 6, CID: 0},
				{TxMin: 7, TxMax: 0, CID: 0},
				{TxMin: 8, TxMax: 9, CID: 0},
				{TxMin: 10, TxMax: 0, CID: 0},
			},
			committed:    []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			xipList:      map[uint64]bool{},
			currentTxID:  10,
			freezeMaxAge: 3,
			expectedDead: 3,
			expectedFrozen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			filename := "test_scan_" + tt.name + ".db"
			createTestHeapFile(t, filename, tt.records)
			defer os.Remove(filename)

			// Create clog
			clogFile := "test_clog_" + tt.name + ".db"
			clog := createTestClog(t, clogFile, tt.committed)
			defer os.Remove(clogFile)
			defer clog.Close()

			// Run ScanHeap
			result, err := ScanHeap(filename, clog, tt.xipList, tt.currentTxID, tt.freezeMaxAge)
			require.NoError(t, err)

			// Verify
			assert.Len(t, result.DeadTIDs, tt.expectedDead, "dead tuples")
			assert.Len(t, result.FrozenTuples, tt.expectedFrozen, "frozen tuples")
		})
	}
}

// =============================================================================
// Tests for MarkDead
// =============================================================================

func TestMarkDead(t *testing.T) {
	// Create a file with 3 records
	records := []storage.TxRecord{
		{TxMin: 1, TxMax: 0, CID: 0},
		{TxMin: 2, TxMax: 0, CID: 0},
		{TxMin: 3, TxMax: 0, CID: 0},
	}
	createTestHeapFile(t, "test_mark.db", records)
	defer os.Remove("test_mark.db")

	// Mark slot 1 as dead
	deadTIDs := []storage.TID{
		{PageId: 1, SlotId: 0}, // page 1, slot 0
	}

	err := MarkDead("test_mark.db", deadTIDs)
	require.NoError(t, err)

	// Verify
	file, err := storage.Open("test_mark.db")
	require.NoError(t, err)
	defer file.Close()

	page, err := file.ReadPage(1)
	require.NoError(t, err)

	assert.True(t, page.IsSlotDead(0), "slot 0 should be dead")
	assert.False(t, page.IsSlotDead(1), "slot 1 should NOT be dead")
	assert.False(t, page.IsSlotDead(2), "slot 2 should NOT be dead")
}

func TestMarkDead_MultipleSlots(t *testing.T) {
	// Create a file with 5 records (5 pages, each with 1 record)
	records := []storage.TxRecord{
		{TxMin: 1, TxMax: 0, CID: 0},
		{TxMin: 2, TxMax: 0, CID: 0},
		{TxMin: 3, TxMax: 0, CID: 0},
		{TxMin: 4, TxMax: 0, CID: 0},
		{TxMin: 5, TxMax: 0, CID: 0},
	}
	createTestHeapFile(t, "test_mark.db", records)
	defer os.Remove("test_mark.db")

	// Mark multiple slots as dead (each record is on its own page)
	deadTIDs := []storage.TID{
		{PageId: 0, SlotId: 0}, // page 0, slot 0
		{PageId: 2, SlotId: 0}, // page 2, slot 0
		{PageId: 4, SlotId: 0}, // page 4, slot 0
	}

	err := MarkDead("test_mark.db", deadTIDs)
	require.NoError(t, err)

	// Verify
	file, err := storage.Open("test_mark.db")
	require.NoError(t, err)
	defer file.Close()

	// Page 0: slot 0 dead
	page0, _ := file.ReadPage(0)
	assert.True(t, page0.IsSlotDead(0), "page 0, slot 0 should be dead")

	// Page 1: slot 0 alive
	page1, _ := file.ReadPage(1)
	assert.False(t, page1.IsSlotDead(0), "page 1, slot 0 should be alive")

	// Page 2: slot 0 dead
	page2, _ := file.ReadPage(2)
	assert.True(t, page2.IsSlotDead(0), "page 2, slot 0 should be dead")

	// Page 3: slot 0 alive
	page3, _ := file.ReadPage(3)
	assert.False(t, page3.IsSlotDead(0), "page 3, slot 0 should be alive")

	// Page 4: slot 0 dead
	page4, _ := file.ReadPage(4)
	assert.True(t, page4.IsSlotDead(0), "page 4, slot 0 should be dead")
}

// =============================================================================
// Tests for ApplyFreezes
// =============================================================================

func TestApplyFreezes(t *testing.T) {
	// Create a file with 2 records
	records := []storage.TxRecord{
		{TxMin: 10, TxMax: 0, CID: 0},
		{TxMin: 20, TxMax: 0, CID: 0},
	}
	createTestHeapFile(t, "test_freeze.db", records)
	defer os.Remove("test_freeze.db")

	// Freeze both tuples
	frozenTuples := []FrozenTuple{
		{TID: storage.TID{PageId: 0, SlotId: 0}, OldMin: 10},
		{TID: storage.TID{PageId: 1, SlotId: 0}, OldMin: 20},
	}

	err := ApplyFreezes("test_freeze.db", frozenTuples)
	require.NoError(t, err)

	// Verify
	file, err := storage.Open("test_freeze.db")
	require.NoError(t, err)
	defer file.Close()

	// Check page 0, slot 0
	page0, _ := file.ReadPage(0)
	rec0, _ := page0.GetRecord(0)
	txRec0, _ := storage.DecodeTxRecord(rec0)
	assert.Equal(t, FrozenTxID, txRec0.TxMin, "TxMin should be frozen")

	// Check page 1, slot 0
	page1, _ := file.ReadPage(1)
	rec1, _ := page1.GetRecord(0)
	txRec1, _ := storage.DecodeTxRecord(rec1)
	assert.Equal(t, FrozenTxID, txRec1.TxMin, "TxMin should be frozen")
}

func TestApplyFreezes_SkipDeadSlots(t *testing.T) {
	// Create a file with 2 records
	records := []storage.TxRecord{
		{TxMin: 10, TxMax: 0, CID: 0},
		{TxMin: 20, TxMax: 0, CID: 0},
	}
	createTestHeapFile(t, "test_freeze.db", records)
	defer os.Remove("test_freeze.db")

	// Mark slot 0 as dead, then try to freeze it
	MarkDead("test_freeze.db", []storage.TID{{PageId: 0, SlotId: 0}})

	frozenTuples := []FrozenTuple{
		{TID: storage.TID{PageId: 0, SlotId: 0}, OldMin: 10}, // dead, should be skipped
		{TID: storage.TID{PageId: 1, SlotId: 0}, OldMin: 20}, // alive, should be frozen
	}

	err := ApplyFreezes("test_freeze.db", frozenTuples)
	require.NoError(t, err)

	// Verify
	file, err := storage.Open("test_freeze.db")
	require.NoError(t, err)
	defer file.Close()

	// Page 0: slot 0 is dead, should not be frozen
	page0, _ := file.ReadPage(0)
	assert.True(t, page0.IsSlotDead(0), "slot 0 should be dead")

	// Page 1: slot 0 should be frozen
	page1, _ := file.ReadPage(1)
	rec1, _ := page1.GetRecord(0)
	txRec1, _ := storage.DecodeTxRecord(rec1)
	assert.Equal(t, FrozenTxID, txRec1.TxMin, "TxMin should be frozen")
}

// =============================================================================
// Integration test: Full VACUUM flow
// =============================================================================

func TestFullVacuumFlow(t *testing.T) {
	// Setup - create 5 records (5 pages, each with 1 record)
	records := []storage.TxRecord{
		{TxMin: 1, TxMax: 0, CID: 0},  // page 0, alive, old → freeze
		{TxMin: 2, TxMax: 3, CID: 0},  // page 1, dead (tx 3 committed)
		{TxMin: 4, TxMax: 0, CID: 0},  // page 2, alive, old → freeze
		{TxMin: 5, TxMax: 6, CID: 0},  // page 3, dead (tx 6 committed)
		{TxMin: 7, TxMax: 0, CID: 0},  // page 4, alive, new → don't freeze
	}
	createTestHeapFile(t, "test_vacuum.db", records)
	defer os.Remove("test_vacuum.db")

	clog := createTestClog(t, "test_clog.db", []uint64{1, 2, 3, 4, 5, 6, 7})
	defer os.Remove("test_clog.db")
	defer clog.Close()

	// Step 1: Scan
	// freezeMaxAge=3, currentTxID=10
	// cutoff=7, freeze if TxMin < 7
	result, err := ScanHeap("test_vacuum.db", clog, map[uint64]bool{}, 10, 3)
	require.NoError(t, err)

	assert.Len(t, result.DeadTIDs, 2, "2 dead tuples")
	assert.Len(t, result.FrozenTuples, 2, "2 frozen tuples (TxMin=1 and TxMin=4)")

	// Step 2: Mark dead
	err = MarkDead("test_vacuum.db", result.DeadTIDs)
	require.NoError(t, err)

	// Step 3: Apply freezes
	err = ApplyFreezes("test_vacuum.db", result.FrozenTuples)
	require.NoError(t, err)

	// Verify final state
	file, err := storage.Open("test_vacuum.db")
	require.NoError(t, err)
	defer file.Close()

	// Page 0: alive, frozen
	page0, _ := file.ReadPage(0)
	assert.False(t, page0.IsSlotDead(0), "page 0, slot 0 alive")
	rec0, _ := page0.GetRecord(0)
	txRec0, _ := storage.DecodeTxRecord(rec0)
	assert.Equal(t, FrozenTxID, txRec0.TxMin, "TxMin frozen")

	// Page 1: dead
	page1, _ := file.ReadPage(1)
	assert.True(t, page1.IsSlotDead(0), "page 1, slot 0 dead")

	// Page 2: alive, frozen
	page2, _ := file.ReadPage(2)
	assert.False(t, page2.IsSlotDead(0), "page 2, slot 0 alive")
	rec2, _ := page2.GetRecord(0)
	txRec2, _ := storage.DecodeTxRecord(rec2)
	assert.Equal(t, FrozenTxID, txRec2.TxMin, "TxMin frozen")

	// Page 3: dead
	page3, _ := file.ReadPage(3)
	assert.True(t, page3.IsSlotDead(0), "page 3, slot 0 dead")

	// Page 4: alive, NOT frozen (TxMin=7 >= cutoff=7)
	page4, _ := file.ReadPage(4)
	assert.False(t, page4.IsSlotDead(0), "page 4, slot 0 alive")
	rec4, _ := page4.GetRecord(0)
	txRec4, _ := storage.DecodeTxRecord(rec4)
	assert.NotEqual(t, FrozenTxID, txRec4.TxMin, "TxMin NOT frozen")
}

// =============================================================================
// Tests for VacuumIndexes (table-driven)
// =============================================================================

func TestVacuumIndexes(t *testing.T) {
	tests := []struct {
		name          string
		treeKeys      []int
		treeTIDs      []storage.TID
		deadTIDs      []storage.TID
		wantRemoved   int
		wantRemaining []int
	}{
		{
			name: "Remove dead index entries",
			treeKeys: []int{100, 200, 300, 400},
			treeTIDs: []storage.TID{
				{PageId: 0, SlotId: 1},
				{PageId: 1, SlotId: 0},
				{PageId: 2, SlotId: 1},
				{PageId: 3, SlotId: 2},
			},
			deadTIDs: []storage.TID{
				{PageId: 1, SlotId: 0},
				{PageId: 3, SlotId: 2},
			},
			wantRemoved:   2,
			wantRemaining: []int{100, 300},
		},
		{
			name: "TID not in index",
			treeKeys: []int{100, 200},
			treeTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 1, SlotId: 0},
			},
			deadTIDs: []storage.TID{
				{PageId: 5, SlotId: 5},
			},
			wantRemoved:   0,
			wantRemaining: []int{100, 200},
		},
		{
			name:          "Empty tree",
			treeKeys:      []int{},
			treeTIDs:      []storage.TID{},
			deadTIDs:      []storage.TID{{PageId: 0, SlotId: 0}},
			wantRemoved:   0,
			wantRemaining: []int{},
		},
		{
			name: "Multiple dead TIDs",
			treeKeys: []int{10, 20, 30},
			treeTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 0, SlotId: 1},
				{PageId: 0, SlotId: 2},
			},
			deadTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 0, SlotId: 2},
			},
			wantRemoved:   2,
			wantRemaining: []int{20},
		},
		{
			name: "All entries dead",
			treeKeys: []int{100, 200},
			treeTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 1, SlotId: 0},
			},
			deadTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 1, SlotId: 0},
			},
			wantRemoved:   2,
			wantRemaining: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			bt := btree.NewBTree()
			for i, key := range tt.treeKeys {
				bt.Insert(key, tt.treeTIDs[i])
			}

			// Act
			removed := VacuumIndexes(bt, tt.deadTIDs)

			// Assert
			assert.Equal(t, tt.wantRemoved, removed)
			for _, key := range tt.wantRemaining {
				_, found := bt.Search(key)
				assert.True(t, found, "key %d should remain", key)
			}
			// Verify dead keys are gone
			for _, key := range tt.treeKeys {
				found := false
				for _, remainKey := range tt.wantRemaining {
					if key == remainKey {
						found = true
						break
					}
				}
				if !found {
					_, exists := bt.Search(key)
					assert.False(t, exists, "key %d should be removed", key)
				}
			}
		})
	}
}

// =============================================================================
// Tests for VacuumHeap (table-driven)
// =============================================================================

func TestVacuumHeap(t *testing.T) {
	tests := []struct {
		name           string
		records        []storage.TxRecord
		deadTIDs       []storage.TID
		initialFSM     []uint16
		wantDead       []int
		wantFSMUpdated bool
	}{
		{
			name: "Mark dead and update FSM",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"alive"}},
				{TxMin: 2, TxMax: 3, CID: 0, Data: storage.Tuple{"dead"}},
			},
			deadTIDs:       []storage.TID{{PageId: 0, SlotId: 1}},
			initialFSM:     []uint16{4000},
			wantDead:       []int{1},
			wantFSMUpdated: true,
		},
		{
			name: "Mark dead without FSM",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"alive"}},
				{TxMin: 2, TxMax: 3, CID: 0, Data: storage.Tuple{"dead"}},
			},
			deadTIDs:       []storage.TID{{PageId: 0, SlotId: 1}},
			initialFSM:     nil,
			wantDead:       []int{1},
			wantFSMUpdated: false,
		},
		{
			name: "No dead tuples",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"alive"}},
			},
			deadTIDs:       []storage.TID{},
			initialFSM:     []uint16{4000},
			wantDead:       []int{},
			wantFSMUpdated: false,
		},
		{
			name: "Multiple pages with dead tuples",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 0, CID: 0, Data: storage.Tuple{"page0-alive"}},
				{TxMin: 2, TxMax: 3, CID: 0, Data: storage.Tuple{"page0-dead"}},
			},
			deadTIDs: []storage.TID{
				{PageId: 0, SlotId: 1},
			},
			initialFSM:     []uint16{4000},
			wantDead:       []int{1},
			wantFSMUpdated: true,
		},
		{
			name: "All tuples dead",
			records: []storage.TxRecord{
				{TxMin: 1, TxMax: 2, CID: 0, Data: storage.Tuple{"dead1"}},
				{TxMin: 3, TxMax: 4, CID: 0, Data: storage.Tuple{"dead2"}},
			},
			deadTIDs: []storage.TID{
				{PageId: 0, SlotId: 0},
				{PageId: 0, SlotId: 1},
			},
			initialFSM:     []uint16{4000},
			wantDead:       []int{0, 1},
			wantFSMUpdated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			heapFile := "test_vacuum_heap.db"
			fsmFile := "test_vacuum_heap.fsm"

			// Create heap file
			createTestHeapFileSinglePage(t, heapFile, tt.records)

			// Create FSM if needed
			if tt.initialFSM != nil {
				fsm, err := storage.CreateFSM(fsmFile)
				require.NoError(t, err)
				for _, free := range tt.initialFSM {
					fsm.AddPage(free)
				}
				fsm.Close()
			}

			// Act
			fsmPath := ""
			if tt.wantFSMUpdated {
				fsmPath = fsmFile
			}
			err := VacuumHeap(heapFile, tt.deadTIDs, fsmPath)

			// Assert
			assert.NoError(t, err)

			// Verify dead tuples marked
			file, err := storage.Open(heapFile)
			require.NoError(t, err)
			defer file.Close()

			for _, slotIdx := range tt.wantDead {
				page, err := file.ReadPage(0)
				require.NoError(t, err)
				assert.True(t, page.IsSlotDead(slotIdx), "slot %d should be dead", slotIdx)
			}

			// Verify FSM updated
			if tt.wantFSMUpdated && len(tt.initialFSM) > 0 {
				fsm, err := storage.OpenFSM(fsmFile)
				require.NoError(t, err)
				defer fsm.Close()

				// Free space should be greater than initial
				initialFree := tt.initialFSM[0]
				currentFree := fsm.Get(0)
				assert.Greater(t, int(currentFree), int(initialFree),
					"FSM should show more free space after marking dead")
			}

			// Cleanup
			os.Remove(heapFile)
			os.Remove(fsmFile)
		})
	}
}

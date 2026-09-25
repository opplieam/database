package vacuum

import (
	"fmt"
	"database/storage"
	"database/tx"
	"database/btree"
)

const FrozenTxID = uint64(2)

// ScanResult holds the results of scanning a heap file.
type ScanResult struct {
	DeadTIDs     []storage.TID
	FrozenTuples []FrozenTuple
}

// FrozenTuple represents a tuple that needs to be frozen.
// Only LIVE tuples are frozen (dead tuples are removed by VACUUM).
type FrozenTuple struct {
	TID    storage.TID              // Where the tuple is (page + slot)
	OldMin uint64                   // Original TxMin value (before freezing)
}

// ScanHeap scans a heap file to find dead tuples and tuples to freeze.
//
// Dead tuple: TxMax != 0 && committed && not in xipList
// Freeze tuple: TxMin is "old" (only for LIVE tuples)
//
// How freeze works:
//   - freezeMaxAge: how many transactions before a tuple is considered "old"
//   - In PostgreSQL: autovacuum_freeze_max_age (default 100M transactions)
//   - Tuples older than freezeMaxAge get frozen to prevent wraparound
//
// Example:
//   currentTxID = 150000000
//   freezeMaxAge = 100000000 (100M)
//   Tuples with TxMin < 50M will be frozen
func ScanHeap(filename string, clog *tx.CommitLog, xipList map[uint64]bool, currentTxID uint64, freezeMaxAge uint64) (*ScanResult, error) {
	file, err := storage.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := &ScanResult{}

	for pageId := uint32(0); pageId < file.PageCount(); pageId++ {
		page, err := file.ReadPage(pageId)
		if err != nil {
			return nil, err
		}

		for slotId := 0; slotId < int(page.Header.RecordCount); slotId++ {
			// Skip already dead slots
			if page.IsSlotDead(slotId) {
				continue
			}

			recordBytes, err := page.GetRecord(slotId)
			if err != nil {
				return nil, err
			}

			txRecord, err := storage.DecodeTxRecord(recordBytes)
			if err != nil {
				return nil, err
			}

			// Check if dead
			if isDead(txRecord, clog, xipList) {
				result.DeadTIDs = append(result.DeadTIDs, storage.TID{
					PageId: pageId,
					SlotId: uint16(slotId),
				})
			} else if shouldFreeze(txRecord.TxMin, currentTxID, freezeMaxAge) {
				// Only freeze LIVE tuples (not dead ones)
				// Dead tuples will be removed by VACUUM anyway
				result.FrozenTuples = append(result.FrozenTuples, FrozenTuple{
					TID:    storage.TID{PageId: pageId, SlotId: uint16(slotId)},
					OldMin: txRecord.TxMin,
				})
			}
		}
	}

	return result, nil
}

// isDead checks if a tuple is dead.
//
// A tuple is dead if:
//  1. TxMax != 0 (has been deleted/updated)
//  2. TxMax is committed (visible in clog)
//  3. TxMax not in xipList (not in-progress)
func isDead(txRecord storage.TxRecord, clog *tx.CommitLog, xipList map[uint64]bool) bool {
	// Not dead if alive (TxMax == 0)
	if txRecord.TxMax == 0 {
		return false
	}

	// Not dead if deleter is in xipList (delete in progress)
	if xipList[txRecord.TxMax] {
		return false
	}

	// Not dead if deleter hasn't committed
	if !clog.IsCommitted(txRecord.TxMax) {
		return false
	}

	// Dead: TxMax set, committed, not in xipList
	return true
}

// shouldFreeze checks if a tuple should be frozen.
//
// A tuple should be frozen if its TxMin is "old" (older than freezeMaxAge).
// This prevents transaction ID wraparound.
//
// How wraparound works:
//   - TxIDs are unsigned integers that circle back to 0
//   - Without freeze, old tuples become invisible after wraparound
//   - Freeze sets TxMin to FrozenTxID (2), making them always visible
//
// The logic:
//   cutoff = currentTxID - freezeMaxAge
//   If cutoff > currentTxID, wraparound happened
//   Freeze if: TxMin is NOT in the new zone
//
// Example with small numbers (max=10, freezeMaxAge=3, currentTxID=2):
//   cutoff = 2 - 3 = -1 → wraps to 18446744073709551615
//   New zone (keep): {0, 1, 2} (recent transactions)
//   Old zone (freeze): {3, 4, 5, 6, 7, 8, 9} (older than 3 transactions)
func shouldFreeze(txMin uint64, currentTxID uint64, freezeMaxAge uint64) bool {
	cutoff := currentTxID - freezeMaxAge

	// Check if wraparound happened
	// If cutoff > currentTxID, we wrapped around
	if cutoff > currentTxID {
		// Wraparound: new zone is {0, 1, ..., currentTxID}
		// Freeze if txMin > currentTxID (old transaction)
		return txMin > currentTxID
	}

	// Normal case: new zone is {cutoff, ..., currentTxID}
	// Freeze if txMin < cutoff (too old)
	return txMin < cutoff
}

// ApplyFreezes updates TxMin for frozen tuples.
//
// This modifies records in-place. TxMin is fixed 8 bytes,
// so record length won't change.
func ApplyFreezes(filename string, frozenTuples []FrozenTuple) error {
	file, err := storage.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Group by page for efficiency
	pageFrozenSlots := make(map[uint32][]uint16)
	for _, ft := range frozenTuples {
		pageFrozenSlots[ft.TID.PageId] = append(pageFrozenSlots[ft.TID.PageId], ft.TID.SlotId)
	}

	for pageId, slotIds := range pageFrozenSlots {
		page, err := file.ReadPage(pageId)
		if err != nil {
			return err
		}

		for _, slotId := range slotIds {
			if err := page.FreezeTuple(int(slotId), FrozenTxID); err != nil {
				// If freeze fails, skip this tuple
				continue
			}
		}

		if err := file.WritePage(pageId, page); err != nil {
			return err
		}
	}

	return nil
}

// MarkDead marks dead tuples in the heap file.
//
// This is NORMAL VACUUM behavior:
//   - Dead tuples are marked, NOT moved
//   - Space becomes available for new INSERTs
//   - Fast, minimal I/O
//
// VACUUM FULL would rewrite the entire table (we don't implement this).
// VACUUM FULL compacts pages by moving records; normal VACUUM just marks slots.
func MarkDead(filename string, deadTIDs []storage.TID) error {
	file, err := storage.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Group by page for efficiency
	pageDeadSlots := make(map[uint32][]uint16)
	for _, tid := range deadTIDs {
		pageDeadSlots[tid.PageId] = append(pageDeadSlots[tid.PageId], tid.SlotId)
	}

	for pageId, slotIds := range pageDeadSlots {
		page, err := file.ReadPage(pageId)
		if err != nil {
			return err
		}

		for _, slotId := range slotIds {
			page.MarkSlotDead(int(slotId))
		}

		if err := file.WritePage(pageId, page); err != nil {
			return err
		}
	}

	return nil
}

// VacuumIndexes removes index entries pointing to dead tuples.
//
// This is Phase 2 of VACUUM. After Phase 1 finds dead tuples,
// Phase 2 removes their index entries to keep indexes consistent.
//
// Returns the number of entries removed.
func VacuumIndexes(bt *btree.BTree, deadTIDs []storage.TID) int {
	removed := 0
	for _, tid := range deadTIDs {
		if bt.DeleteByTID(tid) {
			removed++
		}
	}
	if removed > 0 {
		fmt.Printf("Vacuum: removed %d orphaned index entries\n", removed)
	}
	return removed
}

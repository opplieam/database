// Package tx manages transaction identity and lifecycle: it hands out ids,
// tracks which transactions are in flight, chooses the snapshot a read uses
// under each isolation level, and records how each transaction ended.
package tx

import "maps"

// FrozenTxID marks a row created before every transaction. Vacuum rewrites
// old live rows to this id so they stay visible after their creator's commit
// status ages out of the log. It is reserved: Begin never hands it out.
const FrozenTxID uint64 = 0

// Before reports whether id a comes before id b in the circular id space.
// Ids run 1..MaxUint64 and wrap back to 1, so a plain < would put every
// pre-wrap id after every post-wrap id. Comparing the unsigned distance to
// half the id space keeps the order right while the two ids are within 2^63
// transactions of each other, the same trade-off PostgreSQL makes with a
// 2^31 window over 32-bit ids.
//
// Example:
//
//	Before(MaxUint64, 1) → true
//	Before(1, MaxUint64) → false
func Before(a, b uint64) bool {
	return a != b && b-a < 1<<63
}

// Age returns the distance from id to now in the circular id space. Unsigned
// subtraction stays right across the wrap, where now is numerically smaller
// than id.
//
// Example (the counter wrapped from MaxUint64 to 1):
//
//	Age(MaxUint64, 1) → 2
func Age(from, now uint64) uint64 {
	return now - from
}

// Status is where a transaction stands in its lifecycle. Status changes once:
// a transaction starts active and ends committed or aborted.
type Status int

const (
	StatusActive    Status = iota // running; its writes are not visible yet
	StatusCommitted               // finished; its writes are visible
	StatusAborted                 // finished; its writes are discarded
)

// Isolation decides which snapshot a transaction reads through: one fixed
// snapshot for the whole transaction, or a fresh one per statement.
type Isolation int

const (
	// RepeatableRead takes one snapshot at Begin, so every statement sees
	// the same rows and the same in-progress set.
	RepeatableRead Isolation = iota

	// ReadCommitted takes a new snapshot for every statement, so a commit
	// that happens in between becomes visible mid-transaction.
	ReadCommitted
)

// Snapshot is the read view of one statement. TxID identifies the reader,
// whose own uncommitted writes are visible to it. Cutoff bounds the ids the
// view includes: an id at or above Cutoff started after the view was taken.
// Xip holds the ids that were in flight at that moment.
//
// TxID and Cutoff are separate because they only coincide under
// RepeatableRead. ReadCommitted reads at a cutoff unrelated to its own id.
type Snapshot struct {
	TxID   uint64
	Cutoff uint64
	Xip    map[uint64]bool
}

// Transaction is one unit of work: the statements between Begin and
// Commit/Rollback, which take effect together or not at all. Manager.Begin is
// the only way to create one and Manager.Commit or Manager.Rollback the only
// way to finish it; holding a *Transaction does not keep it active.
//
// Example lifecycle:
//
//	mgr := NewManager()
//	tx := mgr.Begin(RepeatableRead)
//	tx.Status() → StatusActive
//	// ... do work ...
//	mgr.Commit(tx)
//	tx.Status() → StatusCommitted
type Transaction struct {
	id             uint64
	status         Status
	cid            uint32 // command id counter (0, 1, 2, ...)
	isolationLevel Isolation
	xipList        map[uint64]bool // in-progress txs at snapshot time
}

// newTransaction builds an active transaction with the given id and isolation
// level. Only Manager.Begin should call it: Begin also registers the
// transaction and advances the id counter, which is what keeps ids unique and
// snapshots accurate.
func newTransaction(id uint64, isolationLevel Isolation) *Transaction {
	return &Transaction{id: id, status: StatusActive, isolationLevel: isolationLevel}
}

// ID returns the transaction id.
func (t *Transaction) ID() uint64 {
	return t.id
}

// Status returns the transaction status.
func (t *Transaction) Status() Status {
	return t.status
}

// NextCID returns the next command id and advances the counter. Command ids
// number one transaction's statements from zero, so a record can be traced
// back to the statement that wrote it.
//
// Example:
//
//	tx.NextCID() → 0  (first INSERT)
//	tx.NextCID() → 1  (second INSERT)
//	tx.NextCID() → 2  (third INSERT)
func (t *Transaction) NextCID() uint32 {
	cid := t.cid
	t.cid++
	return cid
}

// commit marks the transaction committed. Manager.Commit calls it and also
// removes the transaction from the active set; calling it alone would leave a
// finished transaction looking active.
func (t *Transaction) commit() {
	t.status = StatusCommitted
}

// rollback marks the transaction aborted. Manager.Rollback calls it and also
// removes the transaction from the active set.
func (t *Transaction) rollback() {
	t.status = StatusAborted
}

// Manager hands out transaction ids and keeps the set of transactions that are
// still in flight. Begin is the only way to start a transaction; Commit and
// Rollback are the only ways to finish one, and both keep the active set in
// step with the transaction status.
//
// Example:
//
//	mgr := NewManager()
//	tx1 := mgr.Begin(RepeatableRead)  // ID 1
//	tx2 := mgr.Begin(ReadCommitted)   // ID 2
//	mgr.Commit(tx1)
//	mgr.IsActive(1) → false
//	mgr.IsActive(2) → true
type Manager struct {
	nextID uint64
	epoch  uint64
	active map[uint64]*Transaction
}

// NewManager returns a manager whose first transaction id is 1.
func NewManager() *Manager {
	return &Manager{
		nextID: 1,
		active: make(map[uint64]*Transaction),
	}
}

// Begin allocates the next id, never the reserved 0, records the transactions
// currently in flight as this transaction's snapshot, and adds the new
// transaction to the active set. Finish it with Commit or Rollback. Spending
// MaxUint64 advances the epoch before returning, so the next id is 1 of the
// new epoch and no checkpoint ever stores 0.
func (m *Manager) Begin(isolationLevel Isolation) *Transaction {
	id := m.nextID

	tx := newTransaction(id, isolationLevel)

	// Capture in-progress transactions at snapshot time
	tx.xipList = make(map[uint64]bool)
	for activeID := range m.active {
		tx.xipList[activeID] = true
	}

	m.active[id] = tx
	m.nextID = id + 1
	if m.nextID == FrozenTxID {
		// MaxUint64 is spent. Step straight over the frozen marker on this
		// call, so NextID is always checkpointable and the epoch already
		// names the epoch the next id belongs to.
		m.nextID = 1
		m.epoch++
	}
	return tx
}

// Snapshot returns the read view for the transaction's next statement.
//
// RepeatableRead: the view captured at Begin, cut off at the transaction's own
// id. A transaction with a larger id started later, so its rows stay out.
//
// ReadCommitted: a fresh view on every call. The cutoff is nextID, the first
// id not yet issued, and the in-flight set is refreshed. A commit between
// statements therefore becomes visible: its id is below the cutoff and it has
// left Xip.
func (m *Manager) Snapshot(tx *Transaction) Snapshot {
	if tx.isolationLevel == ReadCommitted {
		return Snapshot{
			TxID:   tx.id,
			Cutoff: m.nextID,
			Xip:    m.activeIDs(tx.id),
		}
	}
	return Snapshot{
		TxID:   tx.id,
		Cutoff: tx.id,
		Xip:    maps.Clone(tx.xipList),
	}
}

// Commit marks the transaction committed and removes it from the active set.
// It does not write the commit log: the executor calls Clog.LogCommit once the
// WAL record is durable.
func (m *Manager) Commit(tx *Transaction) {
	tx.commit()
	delete(m.active, tx.ID())
}

// NextID returns the next transaction id to hand out without allocating
// it. Checkpoint saves it with the epoch, so recovery restores both the
// counter and the epoch it belongs to.
func (m *Manager) NextID() uint64 { return m.nextID }

// Epoch returns how many times the id counter has gone around. It rises when
// Begin spends MaxUint64 and when recovery adopts a checkpoint from a later
// epoch.
func (m *Manager) Epoch() uint64 { return m.epoch }

// Restore adopts a counter pair from recovery. A higher epoch wins even when
// its id is numerically lower, which is what a wrap produces. Within one
// epoch only a higher id moves the counter, so a stale pre-wrap checkpoint
// cannot rewind it.
func (m *Manager) Restore(next, epoch uint64) {
	m.advance(next, epoch)
}

// advance is the one place that compares counter pairs. 0 is the frozen
// marker, never a counter value, so it is ignored.
func (m *Manager) advance(next, epoch uint64) {
	if next == FrozenTxID {
		return
	}
	if epoch > m.epoch {
		m.epoch = epoch
		m.nextID = next
		return
	}
	if epoch == m.epoch && next > m.nextID {
		m.nextID = next
	}
}

// Rollback marks the transaction aborted and removes it from the active set.
// Nothing reaches the commit log, so its rows stay invisible to everyone.
func (m *Manager) Rollback(tx *Transaction) {
	tx.rollback()
	delete(m.active, tx.ID())
}

// IsActive reports whether the id is still in the active set, meaning its
// transaction has not committed or aborted yet.
func (m *Manager) IsActive(txID uint64) bool {
	_, ok := m.active[txID]
	return ok
}

// activeIDs returns the ids currently in flight, excluding excludeTxID. The
// result is a copy, so the caller may keep or modify it.
func (m *Manager) activeIDs(excludeTxID uint64) map[uint64]bool {
	result := make(map[uint64]bool)
	for id := range m.active {
		if id != excludeTxID {
			result[id] = true
		}
	}
	return result
}

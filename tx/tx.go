// Package tx manages transaction identity and lifecycle: it hands out ids,
// tracks which transactions are in flight, chooses the snapshot a read uses
// under each isolation level, and records how each transaction ended.
package tx

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
	stmtCounter    uint64 // statement counter for ReadCommitted
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

// newStatement advances the statement counter and returns the new snapshot ID.
// Under ReadCommitted it also refreshes xipList to the transactions currently
// in flight, which is what makes each statement see a new snapshot.
//
// Callers should reach this through SnapshotID: a RepeatableRead transaction
// must keep the xipList it captured at Begin.
func (t *Transaction) newStatement(mgr *Manager) uint64 {
	t.stmtCounter++
	if t.isolationLevel == ReadCommitted {
		t.xipList = mgr.ActiveTxIDs(t.id)
	}
	return t.stmtCounter
}

// SnapshotID returns the snapshot a read should use.
//
// RepeatableRead: returns tx.ID(), so every statement reads the same snapshot.
// ReadCommitted: returns a new statement ID from newStatement, so each
// statement reads a fresh one; that call also refreshes xipList.
func (t *Transaction) SnapshotID(mgr *Manager) uint64 {
	if t.isolationLevel == ReadCommitted {
		return t.newStatement(mgr)
	}
	return t.id
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

// XipList returns the ids that were in flight when this transaction took its
// snapshot. The map is the transaction's own, not a copy, so callers must not
// modify it.
func (t *Transaction) XipList() map[uint64]bool {
	return t.xipList
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
	active map[uint64]*Transaction
}

// NewManager returns a manager whose first transaction id is 1.
func NewManager() *Manager {
	return &Manager{
		nextID: 1,
		active: make(map[uint64]*Transaction),
	}
}

// Begin allocates the next id, records the transactions currently in flight as
// this transaction's snapshot, and adds the new transaction to the active set.
// Finish it with Commit or Rollback.
func (m *Manager) Begin(isolationLevel Isolation) *Transaction {
	tx := newTransaction(m.nextID, isolationLevel)

	// Capture in-progress transactions at snapshot time
	tx.xipList = make(map[uint64]bool)
	for id := range m.active {
		tx.xipList[id] = true
	}

	m.active[m.nextID] = tx
	m.nextID++
	return tx
}

// Commit marks the transaction committed and removes it from the active set.
// It does not write the commit log: the executor calls Clog.LogCommit once the
// WAL record is durable.
func (m *Manager) Commit(tx *Transaction) {
	tx.commit()
	delete(m.active, tx.ID())
}

// NextID returns the next transaction id to hand out without allocating
// it. Checkpoint saves it so recovery restores the counter past reuse.
func (m *Manager) NextID() uint64 { return m.nextID }

// SetNextID moves the counter forward after recovery, so ids the log
// already spent are never handed out a second time. A lower value is
// ignored: rewinding would reuse ids that living rows still carry.
func (m *Manager) SetNextID(next uint64) {
	if next > m.nextID {
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

// ActiveTxIDs returns the ids currently in flight, excluding excludeTxID. The
// result is a copy, so the caller may keep or modify it.
func (m *Manager) ActiveTxIDs(excludeTxID uint64) map[uint64]bool {
	result := make(map[uint64]bool)
	for id := range m.active {
		if id != excludeTxID {
			result[id] = true
		}
	}
	return result
}

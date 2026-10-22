package tx

// Status represents the status of a transaction.
type Status int

const (
	StatusActive    Status = iota // in progress
	StatusCommitted               // successfully finished
	StatusAborted                 // rolled back
)

// Isolation determines how transactions see each other's changes.
type Isolation int

const (
	// RepeatableRead snapshot taken at transaction start.
	// All statements in the transaction see the same data.
	RepeatableRead Isolation = iota

	// ReadCommitted snapshot taken at each statement.
	// Each statement may see different data.
	ReadCommitted
)

// Transaction represents a single transaction.
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

// newTransaction creates a new active transaction with the given id and isolation level.
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

// NextCID returns the current command id and increments for the next command.
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

// newStatement returns a new snapshot ID for ReadCommitted isolation.
// Each call returns a unique, increasing ID.
// It also refreshes xip_list.
//
// NOTE: This is only for ReadCommitted!
// RepeatableRead does NOT call newStatement.
// RepeatableRead uses the fixed xip_list from Begin().
func (t *Transaction) newStatement(mgr *Manager) uint64 {
	t.stmtCounter++
	if t.isolationLevel == ReadCommitted {
		t.xipList = mgr.ActiveTxIDs(t.id)
	}
	return t.stmtCounter
}

// SnapshotID returns the appropriate snapshot ID based on isolation level.
//
// RepeatableRead: returns tx.ID() (same snapshot for entire transaction)
// ReadCommitted: returns tx.newStatement(mgr) (new snapshot per statement)
func (t *Transaction) SnapshotID(mgr *Manager) uint64 {
	if t.isolationLevel == ReadCommitted {
		return t.newStatement(mgr)
	}
	return t.id
}

// commit marks the transaction as committed.
func (t *Transaction) commit() {
	t.status = StatusCommitted
}

// rollback marks the transaction as aborted.
func (t *Transaction) rollback() {
	t.status = StatusAborted
}

// XipList returns the transaction's xip_list.
func (t *Transaction) XipList() map[uint64]bool {
	return t.xipList
}

// Manager manages transaction creation and tracks active transactions.
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

// NewManager creates a new Manager.
func NewManager() *Manager {
	return &Manager{
		nextID: 1,
		active: make(map[uint64]*Transaction),
	}
}

// Begin starts a new transaction with the given isolation level.
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

// Commit commits a transaction and removes it from active tracking.
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

// Rollback aborts a transaction and removes it from active tracking.
func (m *Manager) Rollback(tx *Transaction) {
	tx.rollback()
	delete(m.active, tx.ID())
}

// IsActive returns true if the transaction is still active (not committed/aborted).
func (m *Manager) IsActive(txID uint64) bool {
	_, ok := m.active[txID]
	return ok
}

// ActiveTxIDs returns a copy of active transaction IDs, excluding the specified tx.
func (m *Manager) ActiveTxIDs(excludeTxID uint64) map[uint64]bool {
	result := make(map[uint64]bool)
	for id := range m.active {
		if id != excludeTxID {
			result[id] = true
		}
	}
	return result
}

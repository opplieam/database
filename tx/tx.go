package tx

// TxStatus represents the status of a transaction.
type TxStatus int

const (
	TxStatusActive    TxStatus = iota // in progress
	TxStatusCommitted                 // successfully finished
	TxStatusAborted                   // rolled back
)

// IsolationLevel determines how transactions see each other's changes.
type IsolationLevel int

const (
	// RepeatableRead: snapshot taken at transaction start.
	// All statements in the transaction see the same data.
	RepeatableRead IsolationLevel = iota

	// ReadCommitted: snapshot taken at each statement.
	// Each statement may see different data.
	ReadCommitted
)

// Transaction represents a single transaction.
//
// Example lifecycle:
//
//	tx := NewTransaction(1, RepeatableRead)
//	tx.Status() → TxStatusActive
//	// ... do work ...
//	tx.Commit()
//	tx.Status() → TxStatusCommitted
type Transaction struct {
	id             uint64
	status         TxStatus
	cid            uint32 // command id counter (0, 1, 2, ...)
	stmtCounter    uint64 // statement counter for ReadCommitted
	isolationLevel IsolationLevel
	xipList        map[uint64]bool // in-progress txs at snapshot time
}

// NewTransaction creates a new active transaction with the given id and isolation level.
func NewTransaction(id uint64, isolationLevel IsolationLevel) *Transaction {
	return &Transaction{id: id, status: TxStatusActive, isolationLevel: isolationLevel}
}

// Id returns the transaction id.
func (t *Transaction) Id() uint64 {
	return t.id
}

// Status returns the transaction status.
func (t *Transaction) Status() TxStatus {
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

// NewStatement returns a new snapshot ID for ReadCommitted isolation.
// Each call returns a unique, increasing ID.
// It also refreshes xip_list.
//
// NOTE: This is only for ReadCommitted!
// RepeatableRead does NOT call NewStatement.
// RepeatableRead uses the fixed xip_list from Begin().
func (t *Transaction) NewStatement(mgr *TxManager) uint64 {
	t.stmtCounter++
	if t.isolationLevel == ReadCommitted {
		t.xipList = mgr.ActiveTxIds(t.id)
	}
	return t.stmtCounter
}

// SnapshotId returns the appropriate snapshot ID based on isolation level.
//
// RepeatableRead: returns tx.Id() (same snapshot for entire transaction)
// ReadCommitted: returns tx.NewStatement() (new snapshot per statement)
func (t *Transaction) SnapshotId(mgr *TxManager) uint64 {
	if t.isolationLevel == ReadCommitted {
		return t.NewStatement(mgr)
	}
	return t.id
}

// Commit marks the transaction as committed.
func (t *Transaction) Commit() {
	t.status = TxStatusCommitted
}

// Rollback marks the transaction as aborted.
func (t *Transaction) Rollback() {
	t.status = TxStatusAborted
}

// XipList returns the transaction's xip_list.
func (t *Transaction) XipList() map[uint64]bool {
	return t.xipList
}

// TxManager manages transaction creation and tracks active transactions.
//
// Example:
//
//	mgr := NewTxManager()
//	tx1 := mgr.Begin(RepeatableRead)  // TxId=1
//	tx2 := mgr.Begin(ReadCommitted)   // TxId=2
//	tx1.Commit()
//	mgr.IsActive(1) → false
//	mgr.IsActive(2) → true
type TxManager struct {
	nextId uint64
	active map[uint64]*Transaction
}

// NewTxManager creates a new TxManager.
func NewTxManager() *TxManager {
	return &TxManager{
		nextId: 1,
		active: make(map[uint64]*Transaction),
	}
}

// Begin starts a new transaction with the given isolation level.
func (m *TxManager) Begin(isolationLevel IsolationLevel) *Transaction {
	tx := NewTransaction(m.nextId, isolationLevel)

	// Capture in-progress transactions at snapshot time
	tx.xipList = make(map[uint64]bool)
	for id := range m.active {
		tx.xipList[id] = true
	}

	m.active[m.nextId] = tx
	m.nextId++
	return tx
}

// Commit commits a transaction and removes it from active tracking.
func (m *TxManager) Commit(tx *Transaction) {
	tx.Commit()
	delete(m.active, tx.Id())
}

// Rollback aborts a transaction and removes it from active tracking.
func (m *TxManager) Rollback(tx *Transaction) {
	tx.Rollback()
	delete(m.active, tx.Id())
}

// IsActive returns true if the transaction is still active (not committed/aborted).
func (m *TxManager) IsActive(txId uint64) bool {
	_, ok := m.active[txId]
	return ok
}

// ActiveTxIds returns a copy of active transaction IDs, excluding the specified tx.
func (m *TxManager) ActiveTxIds(excludeTxId uint64) map[uint64]bool {
	result := make(map[uint64]bool)
	for id := range m.active {
		if id != excludeTxId {
			result[id] = true
		}
	}
	return result
}

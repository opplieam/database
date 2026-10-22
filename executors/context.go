package executors

import "database/tx"

// TransactionContext bundles transaction state for executor nodes.
//
// All executors that need MVCC awareness accept this struct.
// It avoids passing 4 separate parameters to every constructor.
//
// Example:
//
//	mgr := tx.NewManager()
//	tx1 := mgr.Begin(tx.RepeatableRead)
//	ctx := &TransactionContext{
//	    Tx:       tx1,
//	    Clog:     clog,
//	    Snapshot: mgr.Snapshot(tx1),
//	}
//	scan := NewHeapFileScan("movies.data", ctx)
type TransactionContext struct {
	Tx       *tx.Transaction
	Clog     *tx.CommitLog
	Snapshot tx.Snapshot
}

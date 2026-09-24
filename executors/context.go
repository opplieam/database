package executors

import "database/tx"

// TransactionContext bundles transaction state for executor nodes.
//
// All executors that need MVCC awareness accept this struct.
// It avoids passing 4 separate parameters to every constructor.
//
// Example:
//
//	ctx := &TransactionContext{
//	    Tx:       tx1,
//	    Clog:     clog,
//	    Snapshot: tx1.Id(),
//	    XipList:  tx1.XipList(),
//	}
//	scan := NewHeapFileScan("movies.data", ctx)
type TransactionContext struct {
	Tx       *tx.Transaction
	Clog     *tx.CommitLog
	Snapshot uint64
	XipList  map[uint64]bool
}

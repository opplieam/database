package executors

import "database/storage"

// decodeVisibleTxRecord decodes an MVCC record and reports whether the current
// transaction can see it. Invisible records are not errors; callers skip them
// and continue scanning.
func decodeVisibleTxRecord(recordBytes []byte, ctx *TransactionContext) (storage.Tuple, bool, error) {
	txRec, err := storage.DecodeTxRecord(recordBytes)
	if err != nil {
		return nil, false, err
	}
	if !txRec.Visible(ctx.Snapshot, ctx.Clog) {
		return nil, false, nil
	}
	return txRec.Data, true, nil
}

// encodeMVCCInsert builds the MVCC record for an insert. Callers write the
// bytes through their own storage path.
func encodeMVCCInsert(ctx *TransactionContext, data storage.Tuple) []byte {
	cid := ctx.Tx.NextCID()
	txRec := storage.InsertTxRecord(ctx.Tx.ID(), cid, data)
	return storage.EncodeTxRecord(txRec)
}

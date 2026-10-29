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

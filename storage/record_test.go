package storage

import (
	"os"
	"testing"

	"database/tx"

	"github.com/stretchr/testify/assert"
)

func TestEncodeDecodeRecord(t *testing.T) {
	tests := []struct {
		name    string
		id      uint32
		title   string
		genres  string
	}{
		{"basic movie", 1, "Toy Story (1995)", "Adventure|Animation|Children|Comedy|Fantasy"},
		{"long title", 2, "Jumanji (1995)", "Adventure|Children|Fantasy"},
		{"short genres", 3, "Heat", "Action"},
		{"empty genres", 4, "Test Movie", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeRecord(tt.id, tt.title, tt.genres)
			decodedId, decodedTitle, decodedGenres, err := DecodeRecord(encoded)

			assert.NoError(t, err)
			assert.Equal(t, tt.id, decodedId)
			assert.Equal(t, tt.title, decodedTitle)
			assert.Equal(t, tt.genres, decodedGenres)
		})
	}
}

func TestEncodeTxRecord(t *testing.T) {
	tests := []struct {
		name   string
		record TxRecord
	}{
		{
			"alive record",
			TxRecord{TxMin: 1, TxMax: 0, CID: 0, Data: Tuple{"A"}},
		},
		{
			"deleted record",
			TxRecord{TxMin: 1, TxMax: 3, CID: 1, Data: Tuple{"B"}},
		},
		{
			"multiple fields",
			TxRecord{TxMin: 5, TxMax: 0, CID: 2, Data: Tuple{uint32(42), "hello"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeTxRecord(tt.record)
			decoded, err := DecodeTxRecord(encoded)

			assert.NoError(t, err)
			assert.Equal(t, tt.record.TxMin, decoded.TxMin)
			assert.Equal(t, tt.record.TxMax, decoded.TxMax)
			assert.Equal(t, tt.record.CID, decoded.CID)
			assert.Equal(t, tt.record.Data, decoded.Data)
		})
	}
}

func setupClog(t *testing.T) *tx.CommitLog {
	t.Helper()
	clogFile := "test_clog_record.data"
	t.Cleanup(func() { os.Remove(clogFile) })

	clog, err := tx.OpenCommitLog(clogFile)
	assert.NoError(t, err)
	t.Cleanup(func() { clog.Close() })

	return clog
}

func TestVisibleOwnTx(t *testing.T) {
	clog := setupClog(t)

	// tx=1 inserts but does NOT commit
	InsertTxRecord(1, 0, Tuple{"A"})

	// tx=1 can see its own uncommitted change
	rec := TxRecord{TxMin: 1, TxMax: 0, CID: 0, Data: Tuple{"A"}}
	assert.True(t, rec.Visible(1, clog), "tx=1 should see own uncommitted change")

	// tx=2 cannot see tx=1's uncommitted change
	assert.False(t, rec.Visible(2, clog), "tx=2 should not see tx=1's uncommitted change")
}

func TestVisibleCommitted(t *testing.T) {
	clog := setupClog(t)

	// tx=1 inserts and commits
	rec := InsertTxRecord(1, 0, Tuple{"A"})
	clog.LogCommit(1)

	// tx=2 should see committed record
	assert.True(t, rec.Visible(2, clog), "tx=2 should see tx=1's committed record")

	// tx=1 should also see it
	assert.True(t, rec.Visible(1, clog), "tx=1 should see own committed record")
}

func TestVisibleDeleted(t *testing.T) {
	clog := setupClog(t)

	// tx=1 inserts and commits
	rec := InsertTxRecord(1, 0, Tuple{"A"})
	clog.LogCommit(1)

	// tx=2 deletes and commits
	deleted := MarkDeleted(rec, 2)
	clog.LogCommit(2)

	// tx=3 should not see deleted record
	assert.False(t, deleted.Visible(3, clog), "tx=3 should not see deleted record")

	// tx=1 (before delete) should still see it
	assert.True(t, deleted.Visible(1, clog), "tx=1 should see record (delete happened after)")
}

func TestInsertTxRecord(t *testing.T) {
	rec := InsertTxRecord(5, 3, Tuple{uint32(1), "Toy Story", "Adventure"})

	assert.Equal(t, uint64(5), rec.TxMin, "TxMin should be the creating transaction")
	assert.Equal(t, uint64(0), rec.TxMax, "TxMax should be 0 (alive)")
	assert.Equal(t, uint32(3), rec.CID, "CID should be the command id")
	assert.Equal(t, Tuple{uint32(1), "Toy Story", "Adventure"}, rec.Data)
}

func TestMarkDeleted(t *testing.T) {
	original := InsertTxRecord(1, 0, Tuple{"A"})
	deleted := MarkDeleted(original, 7)

	assert.Equal(t, uint64(1), deleted.TxMin, "TxMin should be unchanged")
	assert.Equal(t, uint64(7), deleted.TxMax, "TxMax should be the deleting transaction")
	assert.Equal(t, Tuple{"A"}, deleted.Data, "Data should be unchanged")
}

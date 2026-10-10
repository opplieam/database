package wal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round-trip one record per type: build, encode, decode, compare fields.
func TestRecordRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		record *Record
	}{
		{
			name: "insert",
			record: &Record{
				Xid:     7,
				Type:    RecordInsert,
				PrevLSN: 0,
				Payload: PayloadInsert(3, 1, 0, []byte{9, 9, 9}),
			},
		},
		{
			name: "commit",
			record: &Record{
				Xid:     7,
				Type:    RecordCommit,
				PrevLSN: 41,
				Payload: nil,
			},
		},
		{
			name: "checkpoint",
			record: &Record{
				Xid:     0,
				Type:    RecordCheckpoint,
				PrevLSN: 100,
				Payload: PayloadCheckpoint(64, 8),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := DecodeRecord(EncodeRecord(tt.record))
			require.NoError(t, err)
			assert.Equal(t, tt.record.Xid, decoded.Xid)
			assert.Equal(t, tt.record.Type, decoded.Type)
			assert.Equal(t, tt.record.PrevLSN, decoded.PrevLSN)
			assert.Equal(t, tt.record.Payload, decoded.Payload)
		})
	}
}

// Flip one payload byte: CRC must reject the record.
func TestDecodeCorrupt(t *testing.T) {
	rec := &Record{Xid: 1, Type: RecordInsert, Payload: PayloadInsert(0, 0, 0, []byte{1, 2, 3})}
	buf := EncodeRecord(rec)
	buf[22] ^= 0xFF // inside the payload
	_, err := DecodeRecord(buf)
	assert.Error(t, err)
}

// Short and truncated buffers error instead of parsing.
func TestDecodeShort(t *testing.T) {
	_, err := DecodeRecord([]byte{1, 2, 3})
	assert.Error(t, err)

	rec := &Record{Xid: 1, Type: RecordCommit}
	buf := EncodeRecord(rec)
	_, err = DecodeRecord(buf[:len(buf)-1])
	assert.Error(t, err)
}

// Unknown type byte errors even with a valid checksum.
func TestDecodeUnknownType(t *testing.T) {
	rec := &Record{Xid: 1, Type: RecordType(9), Payload: nil}
	_, err := DecodeRecord(EncodeRecord(rec))
	assert.Error(t, err)
}

// Payload helpers split what they build.
func TestPayloadHelpers(t *testing.T) {
	pageId, slot, bitmap, row, err := ParseInsert(PayloadInsert(5, 2, 3, []byte{4, 5}))
	require.NoError(t, err)
	assert.Equal(t, uint32(5), pageId)
	assert.Equal(t, uint16(2), slot)
	assert.Equal(t, uint8(3), bitmap)
	assert.Equal(t, []byte{4, 5}, row)

	redo, nextXid, err := ParseCheckpoint(PayloadCheckpoint(512, 9))
	require.NoError(t, err)
	assert.Equal(t, LSN(512), redo)
	assert.Equal(t, uint64(9), nextXid)
}

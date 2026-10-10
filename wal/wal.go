// Package wal implements Write-Ahead Logging for crash recovery.
//
// ## What we implement
//
// Record bytes: LSN addressing, three record types (insert, commit,
// checkpoint), CRC32 framing. Pure encode/decode, no file I/O; the
// segment file lives in segment.go.
//
// How it works:
//  1. Callers build a Record with a typed payload
//  2. EncodeRecord frames it: header, payload, checksum
//  3. DecodeRecord parses and verifies; corrupt bytes error out
//
// Benefits:
//   - Testable: byte functions need no disk
//   - Safe: CRC turns torn writes into errors, never replayed lies
//
// Limitations:
//   - Three record types only; PostgreSQL has dozens of resource managers
//   - No compression, no full-page images (see skipped)
//
// ## What we skip (not implemented)
//
// Full-page writes, archiving, timelines, background writer, compression.
// We skip them because recovery logic (write path, ordering, replay rule)
// is fully teachable without them; each is a follow-up topic.
package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// LSN is a byte offset into the WAL segment file. LSN 0 means no
// position; the first record starts after the segment header.
type LSN uint64

// Record types. One byte routes replay, collapsing PostgreSQL's
// rmid+info pair into a single tag: three operations exist in total.
type RecordType byte

const (
	RecordInsert     RecordType = 1
	RecordCommit     RecordType = 2
	RecordCheckpoint RecordType = 3
)

// Record header layout, little-endian, 25 bytes total:
//
//	[0-3]   tot_len: entire record including header
//	[4-11]  xid: owning transaction id
//	[12]    type: insert, commit, or checkpoint
//	[13-20] prev_lsn: LSN of previous record (log chain link)
//	[21..]  payload (tot_len - 25 bytes)
//	[last 4] crc32: checksum over every byte before it
// Record header fields occupy 21 bytes; with the trailing checksum the
// smallest record (empty payload) is RecordHeaderSize bytes.
const (
	recordFieldsSize = 21
	RecordHeaderSize = 25
)

// Record is one WAL entry: envelope fields plus opaque payload bytes.
// Payload layout depends on Type; see PayloadInsert and PayloadCheckpoint.
type Record struct {
	Xid     uint64
	Type    RecordType
	PrevLSN LSN
	Payload []byte
}

// EncodeRecord serializes rec. tot_len covers header plus payload; the
// checksum covers every byte before it.
func EncodeRecord(rec *Record) []byte {
	buf := make([]byte, RecordHeaderSize+len(rec.Payload))
	binary.LittleEndian.PutUint32(buf[0:], uint32(len(buf)))
	binary.LittleEndian.PutUint64(buf[4:], rec.Xid)
	buf[12] = byte(rec.Type)
	binary.LittleEndian.PutUint64(buf[13:], uint64(rec.PrevLSN))
	copy(buf[21:], rec.Payload)
	binary.LittleEndian.PutUint32(buf[len(buf)-4:], crc32.ChecksumIEEE(buf[:len(buf)-4]))
	return buf
}

// DecodeRecord parses one record from buf's start. Verifies framing and
// checksum; corrupt or short bytes return an error, never a record.
func DecodeRecord(buf []byte) (*Record, error) {
	if len(buf) < RecordHeaderSize {
		return nil, fmt.Errorf("wal: %d bytes too short for record header", len(buf))
	}
	totLen := binary.LittleEndian.Uint32(buf[0:])
	if uint32(len(buf)) < totLen {
		return nil, fmt.Errorf("wal: %d bytes shorter than record length %d", len(buf), totLen)
	}
	body := buf[:totLen]
	want := binary.LittleEndian.Uint32(body[len(body)-4:])
	if got := crc32.ChecksumIEEE(body[:len(body)-4]); got != want {
		return nil, fmt.Errorf("wal: checksum mismatch, torn or corrupt record")
	}
	recType := RecordType(body[12])
	if recType != RecordInsert && recType != RecordCommit && recType != RecordCheckpoint {
		return nil, fmt.Errorf("wal: unknown record type %d", recType)
	}
	var payload []byte
	if n := len(body) - RecordHeaderSize; n > 0 {
		payload = make([]byte, n)
		copy(payload, body[recordFieldsSize:len(body)-4])
	}
	return &Record{
		Xid:     binary.LittleEndian.Uint64(body[4:]),
		Type:    recType,
		PrevLSN: LSN(binary.LittleEndian.Uint64(body[13:])),
		Payload: payload,
	}, nil
}

// Insert payload layout, little-endian:
//
//	[0-3] page_id: heap page the row landed on
//	[4-5] slot_index: slot within the page
//	[6-7] payload_len: TxRecord byte length
//	[8]   null_bitmap: which columns are NULL (recovery needs it for AddRecord)
//	[9..] TxRecord bytes, encoded by the caller via storage
func PayloadInsert(pageId uint32, slot uint16, bitmap uint8, txRecord []byte) []byte {
	payload := make([]byte, 9+len(txRecord))
	binary.LittleEndian.PutUint32(payload[0:], pageId)
	binary.LittleEndian.PutUint16(payload[4:], slot)
	binary.LittleEndian.PutUint16(payload[6:], uint16(len(txRecord)))
	payload[8] = bitmap
	copy(payload[9:], txRecord)
	return payload
}

// ParseInsert splits an insert payload into page id, slot index, and
// TxRecord bytes. Recovery asserts the replayed slot equals slot index.
func ParseInsert(payload []byte) (uint32, uint16, uint8, []byte, error) {
	if len(payload) < 9 {
		return 0, 0, 0, nil, fmt.Errorf("wal: %d bytes too short for insert payload", len(payload))
	}
	pageId := binary.LittleEndian.Uint32(payload[0:])
	slot := binary.LittleEndian.Uint16(payload[4:])
	recLen := binary.LittleEndian.Uint16(payload[6:])
	if uint16(len(payload[9:])) < recLen {
		return 0, 0, 0, nil, fmt.Errorf("wal: insert payload holds %d bytes, claims %d", len(payload[9:]), recLen)
	}
	return pageId, slot, payload[8], payload[9 : 9+recLen], nil
}

// Checkpoint payload layout, little-endian:
//
//	[0-7] redo_lsn: replay starts here
//	[8-15] next_xid: transaction id counter to restore
func PayloadCheckpoint(redo LSN, nextXid uint64) []byte {
	payload := make([]byte, 16)
	binary.LittleEndian.PutUint64(payload[0:], uint64(redo))
	binary.LittleEndian.PutUint64(payload[8:], nextXid)
	return payload
}

// ParseCheckpoint splits a checkpoint payload into REDO LSN and the
// next transaction id.
func ParseCheckpoint(payload []byte) (LSN, uint64, error) {
	if len(payload) < 16 {
		return 0, 0, fmt.Errorf("wal: %d bytes too short for checkpoint payload", len(payload))
	}
	return LSN(binary.LittleEndian.Uint64(payload[0:])),
		binary.LittleEndian.Uint64(payload[8:]), nil
}

package storage

import (
	"database/tx"
	"encoding/binary"
	"errors"
	"fmt"
)

// Tuple represents a single row as a slice of values.
type Tuple = []any

// TID (Tuple ID) uniquely identifies a tuple in a heap file.
// Used by indexes to point to tuples without storing them.
//
// Example:
//
//	Page 2, Slot 5 → TID{PageId: 2, SlotId: 5}
//
// Usage in B+ tree:
//
//	Leaf node: Keys = [100, 200, 300]
//	Leaf node: TIDs = [{Page:0, Slot:0}, {Page:1, Slot:2}, {Page:2, Slot:1}]
type TID struct {
	PageId uint32
	SlotId uint16
}

// TxRecord wraps a Tuple with MVCC version metadata.
//
// Fields:
//
//	TxMin - transaction that created this version (INSERT)
//	TxMax - transaction that deleted/updated this version (0 = alive)
//	CID   - command id within the transaction (0, 1, 2, ...)
//
// Example: transaction 5 executes three INSERTs
//
//	INSERT INTO movies VALUES (1, 'Toy Story')    → TxRecord{TxMin:5, TxMax:0, CID:0}
//	INSERT INTO movies VALUES (2, 'Jumanji')      → TxRecord{TxMin:5, TxMax:0, CID:1}
//	INSERT INTO movies VALUES (3, 'Heat')         → TxRecord{TxMin:5, TxMax:0, CID:2}
//
// Example: UPDATE movie 1 at transaction 7
//
//	Old version: TxRecord{TxMin:5, TxMax:7, CID:0}  ← marked as deleted
//	New version: TxRecord{TxMin:7, TxMax:0, CID:0}  ← new insert
type TxRecord struct {
	TxMin uint64
	TxMax uint64  // 0 means alive
	CID   uint32  // command id within transaction
	Data  Tuple
}

// EncodeRecord encodes a movie record as bytes:
//
//	[4 bytes] uint32 movieId (little-endian)
//	[1 byte]  uint8 title length
//	[N bytes] title bytes
//	[1 byte]  uint8 genres length
//	[N bytes] genres bytes
func EncodeRecord(movieId uint32, title, genres string) []byte {
	if len(title) > 255 {
		title = title[:255]
	}
	if len(genres) > 255 {
		genres = genres[:255]
	}

	data := make([]byte, 4+1+len(title)+1+len(genres))
	offset := 0

	binary.LittleEndian.PutUint32(data[offset:], movieId)
	offset += 4

	data[offset] = byte(len(title))
	offset++
	copy(data[offset:], title)
	offset += len(title)

	data[offset] = byte(len(genres))
	offset++
	copy(data[offset:], genres)

	return data
}

// DecodeRecord decodes a movie record from bytes.
func DecodeRecord(data []byte) (movieId uint32, title, genres string, err error) {
	if len(data) < 4 {
		return 0, "", "", errors.New("data too short for movieId")
	}

	offset := 0
	movieId = binary.LittleEndian.Uint32(data[offset:])
	offset += 4

	if len(data) < offset+1 {
		return 0, "", "", errors.New("data too short for title length")
	}
	titleLen := int(data[offset])
	offset++

	if len(data) < offset+titleLen {
		return 0, "", "", errors.New("data too short for title")
	}
	title = string(data[offset : offset+titleLen])
	offset += titleLen

	if len(data) < offset+1 {
		return 0, "", "", errors.New("data too short for genres length")
	}
	genresLen := int(data[offset])
	offset++

	if len(data) < offset+genresLen {
		return 0, "", "", errors.New("data too short for genres")
	}
	genres = string(data[offset : offset+genresLen])

	return movieId, title, genres, nil
}

// encodeTuple encodes a Tuple to bytes using type tags.
//
// Format per value:
//
//	[1 byte]  type tag: 1=uint32, 2=string
//	[N bytes] data
//
// Examples:
//
//	Tuple{uint32(42), "hello"}
//	  → [1][42,0,0,0][2][5][h,e,l,l,o]
//	  = 1 + 4 + 1 + 1 + 5 = 12 bytes
//
//	Tuple{"Toy Story", uint32(1995)}
//	  → [2][9][T,o,y, ,S,t,o,r,y][1][1995,7,0,0]
//	  = 1 + 1 + 9 + 1 + 4 = 16 bytes
func encodeTuple(t Tuple) []byte {
	var buf []byte
	for _, v := range t {
		switch val := v.(type) {
		case uint32:
			buf = append(buf, 1) // type tag for uint32
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, val)
			buf = append(buf, b...)
		case string:
			buf = append(buf, 2) // type tag for string
			b := make([]byte, 2+len(val))
			binary.LittleEndian.PutUint16(b, uint16(len(val)))
			copy(b[2:], val)
			buf = append(buf, b...)
		}
	}
	return buf
}

// decodeTuple decodes a Tuple from bytes.
func decodeTuple(data []byte) (Tuple, error) {
	var t Tuple
	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}
		tag := data[offset]
		offset++
		switch tag {
		case 1: // uint32
			if offset+4 > len(data) {
				return nil, errors.New("insufficient data for uint32")
			}
			val := binary.LittleEndian.Uint32(data[offset:])
			t = append(t, val)
			offset += 4
		case 2: // string
			if offset+2 > len(data) {
				return nil, errors.New("insufficient data for string length")
			}
			strLen := binary.LittleEndian.Uint16(data[offset:])
			offset += 2
			if offset+int(strLen) > len(data) {
				return nil, errors.New("insufficient data for string")
			}
			t = append(t, string(data[offset:offset+int(strLen)]))
			offset += int(strLen)
		default:
			return nil, fmt.Errorf("unknown type tag: %d", tag)
		}
	}
	return t, nil
}

// EncodeTxRecord serializes a TxRecord to bytes.
//
// Layout:
//
//	[0-7]   TxMin       (8 bytes, little-endian uint64)
//	[8-15]  TxMax       (8 bytes, little-endian uint64)
//	[16-19] CID         (4 bytes, little-endian uint32)
//	[20-23] Data length (4 bytes, little-endian uint32)
//	[24-..] Data        (N bytes, type-tagged tuple)
//
// Example: TxRecord{TxMin:1, TxMax:0, CID:0, Data: [uint32(42), "hello"]}
//
//	Total size = 24 + 12 = 36 bytes
//	[0-7]   = 0x01 0x00 0x00 0x00 0x00 0x00 0x00 0x00  (TxMin=1)
//	[8-15]  = 0x00 0x00 0x00 0x00 0x00 0x00 0x00 0x00  (TxMax=0)
//	[16-19] = 0x00 0x00 0x00 0x00                       (CID=0)
//	[20-23] = 0x0C 0x00 0x00 0x00                       (Data length=12)
//	[24-35] = encoded tuple data
func EncodeTxRecord(r TxRecord) []byte {
	dataBytes := encodeTuple(r.Data)
	buf := make([]byte, 24+len(dataBytes))

	binary.LittleEndian.PutUint64(buf[0:], r.TxMin)
	binary.LittleEndian.PutUint64(buf[8:], r.TxMax)
	binary.LittleEndian.PutUint32(buf[16:], r.CID)
	binary.LittleEndian.PutUint32(buf[20:], uint32(len(dataBytes)))
	copy(buf[24:], dataBytes)

	return buf
}

// DecodeTxRecord deserializes a TxRecord from bytes.
func DecodeTxRecord(buf []byte) (TxRecord, error) {
	if len(buf) < 24 {
		return TxRecord{}, errors.New("buffer too short for TxRecord header")
	}

	r := TxRecord{}
	r.TxMin = binary.LittleEndian.Uint64(buf[0:])
	r.TxMax = binary.LittleEndian.Uint64(buf[8:])
	r.CID = binary.LittleEndian.Uint32(buf[16:])
	dataLen := binary.LittleEndian.Uint32(buf[20:])

	if len(buf) < 24+int(dataLen) {
		return TxRecord{}, errors.New("buffer too short for Data")
	}

	var err error
	r.Data, err = decodeTuple(buf[24 : 24+int(dataLen)])
	if err != nil {
		return TxRecord{}, err
	}

	return r, nil
}

// Visible determines if this record version is visible to a transaction.
//
// This function works with any isolation level.
// The caller determines the snapshot ID:
//   - REPEATABLE READ: pass tx.Id() (same snapshot for entire transaction)
//   - READ COMMITTED: pass tx.NewStatement() (new snapshot per statement)
//
// The xip_list contains transaction IDs that were in-progress at snapshot time.
// If the creator is in the xip_list, the record is invisible.
//
// Visibility rules (simplified from PostgreSQL):
//
// A record is VISIBLE if:
//   1. It's my own uncommitted change
//   2. The creator is NOT in my xip_list (was not in-progress at snapshot)
//   3. The creator committed before my snapshot
//   4. It's not deleted, OR the delete hasn't happened yet
//
// A record is INVISIBLE if:
//   - The creator is in my xip_list (was in-progress at snapshot)
//   - The creator hasn't committed (and it's not mine)
//   - The creator committed after my snapshot
//   - It was deleted before my snapshot
//
// Examples:
//
//	TxRecord{TxMin: 1, TxMax: 0}, xipList={}, clog has tx=1 committed
//	  Visible(1, clog, xipList) → true   (my own change)
//	  Visible(2, clog, xipList) → true   (created before tx=2, not deleted)
//
//	TxRecord{TxMin: 1, TxMax: 0}, xipList={1: true}, clog has tx=1 committed
//	  Visible(1, clog, xipList) → true   (my own change)
//	  Visible(2, clog, xipList) → false  (tx=1 was in-progress at snapshot)
func (r TxRecord) Visible(currentTxId uint64, clog *tx.CommitLog, xipList map[uint64]bool) bool {
	// My own changes are always visible
	if r.TxMin == currentTxId {
		return true
	}

	// If creator is in my xip_list → was in-progress at my snapshot → invisible
	if xipList[r.TxMin] {
		return false
	}

	// Is the creator committed?
	if !clog.IsCommitted(r.TxMin) {
		return false
	}

	// Was the record created before or at my snapshot?
	if r.TxMin > currentTxId {
		return false
	}

	// Is the record alive (not deleted)?
	if r.TxMax == 0 {
		return true
	}

	// Is the delete committed?
	if !clog.IsCommitted(r.TxMax) {
		return true
	}

	// Was the delete after my snapshot?
	if r.TxMax > currentTxId {
		return true
	}

	// The delete happened before or at my snapshot.
	return false
}

// InsertTxRecord creates a new TxRecord for an INSERT operation.
//
// Example:
//
//	rec := InsertTxRecord(5, 0, Tuple{uint32(1), "Toy Story", "Adventure"})
//	rec.TxMin = 5   (inserted by tx=5)
//	rec.TxMax = 0   (alive)
//	rec.CID = 0     (first command)
//	rec.Data = [1, "Toy Story", "Adventure"]
func InsertTxRecord(txId uint64, cid uint32, data Tuple) TxRecord {
	return TxRecord{
		TxMin: txId,
		TxMax: 0,
		CID:   cid,
		Data:  data,
	}
}

// MarkDeleted marks a TxRecord as deleted by setting TxMax.
//
// Example:
//
//	old := TxRecord{TxMin: 5, TxMax: 0, CID: 0, Data: [...]}
//	deleted := MarkDeleted(old, 7)
//	deleted.TxMin = 5   (unchanged)
//	deleted.TxMax = 7   (deleted by tx=7)
func MarkDeleted(record TxRecord, txId uint64) TxRecord {
	record.TxMax = txId
	return record
}

// UpdateRecord marks old as deleted and returns old + new version.
//
// Returns two TxRecords:
//   - old: TxMax = txId (marked as deleted)
//   - new: TxMin = txId, TxMax = 0 (fresh insert)
//
// Example:
//
//	old := TxRecord{TxMin: 5, TxMax: 0, Data: ["Toy Story"]}
//	newVersion := Tuple{uint32(1), "Toy Story (Remastered)", "Adventure"}
//	oldRecord, newRecord := UpdateRecord(old, 7, 0, newVersion)
//	oldRecord.TxMax = 7   (deleted by tx=7)
//	newRecord.TxMin = 7   (inserted by tx=7)
//	newRecord.TxMax = 0   (alive)
func UpdateRecord(old TxRecord, txId uint64, cid uint32, newData Tuple) (oldRecord, newRecord TxRecord) {
	old.TxMax = txId
	new := TxRecord{
		TxMin: txId,
		TxMax: 0,
		CID:   cid,
		Data:  newData,
	}
	return old, new
}

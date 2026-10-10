// Control file layout, little-endian, 24 bytes:
//
//	[0-7]  magic: "CTL1" + version, rejects non-control files
//	[8-15] checkpoint_lsn: where the checkpoint record lives
//	[16-23] redo_lsn: where replay starts
package wal

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Control magic: fixed stamp plus format version.
var controlStamp = []byte{'C', 'T', 'L', '1'}

const controlVersion = 1

// ControlSize is the control file length: magic plus two LSNs.
const ControlSize = 24

// Flusher writes every dirty page. *buffer.BufferPool satisfies it;
// checkpoint stays decoupled from the pool's type (no import cycle).
type Flusher interface{ FlushAll() error }

// Checkpoint captures REDO as the current log end, flushes dirty pages,
// appends the checkpoint record, and publishes both LSNs in the control
// file. Returns checkpoint LSN and REDO. Single-threaded: nothing appends
// between capture and write, so REDO equals the checkpoint record's LSN;
// replay meets it first and skips it. Control write is tmp plus rename:
// a crash never leaves a half-written publish. The record belongs to no
// transaction, so Xid stays 0.
func Checkpoint(flusher Flusher, seg *Segment, ctlPath string, nextXid uint64) (LSN, LSN, error) {
	redo := seg.End()
	if err := flusher.FlushAll(); err != nil {
		return 0, 0, err
	}
	checkpoint, err := seg.Append(&Record{
		Type:    RecordCheckpoint,
		Payload: PayloadCheckpoint(redo, nextXid),
	})
	if err != nil {
		return 0, 0, err
	}
	if err := seg.Flush(); err != nil {
		return 0, 0, err
	}
	if err := WriteControl(ctlPath, checkpoint, redo); err != nil {
		return 0, 0, err
	}
	return checkpoint, redo, nil
}

// WriteControl publishes checkpoint and REDO atomically via tmp rename.
func WriteControl(path string, checkpoint, redo LSN) error {
	buf := make([]byte, ControlSize)
	copy(buf, controlStamp)
	binary.LittleEndian.PutUint32(buf[4:], controlVersion)
	binary.LittleEndian.PutUint64(buf[8:], uint64(checkpoint))
	binary.LittleEndian.PutUint64(buf[16:], uint64(redo))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadControl loads a control file. Missing or corrupt files error:
// without a start point recovery must refuse, never guess.
func ReadControl(path string) (LSN, LSN, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	if len(buf) != ControlSize || string(buf[:4]) != string(controlStamp) ||
		binary.LittleEndian.Uint32(buf[4:]) != controlVersion {
		return 0, 0, fmt.Errorf("wal: %s is not a WAL control file", path)
	}
	return LSN(binary.LittleEndian.Uint64(buf[8:])),
		LSN(binary.LittleEndian.Uint64(buf[16:])), nil
}

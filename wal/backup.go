// Backup label layout, little-endian, 32 bytes:
//
//	[0-7]   magic: "BKP1" + version, rejects non-label files
//	[8-15]  redo_lsn: replay starts here after a restore
//	[16-23] next_xid: transaction id counter at backup time
//	[24-31] archive_seq: archive holding the log from redo_lsn
package wal

import (
	"encoding/binary"
	"fmt"
	"os"
)

// backupStamp: fixed stamp plus format version, 8 bytes total, matching
// the segment and control stamps.
var backupStamp = []byte{'B', 'K', 'P', '1'}

const backupVersion = 1

// BackupLabelSize is the label length: magic plus three fields.
const BackupLabelSize = 32

// BackupLabel freezes the point a base backup starts replay from. It is
// written once, when the copy begins, and never rewritten, which is what
// separates it from the control file: a checkpoint during the copy moves
// pg_control forward, so a copied control file can name a later start point
// than the backup actually needs.
//
// PITR reads this file where normal recovery reads the control file.
// PostgreSQL writes it as text so an operator can read it during a failed
// restore; ours is binary like every other file here and carries the three
// fields replay needs.
type BackupLabel struct {
	RedoLSN    LSN    // where replay starts: the checkpoint at copy time
	NextXid    uint64 // counter to restore, so spent ids stay spent
	ArchiveSeq uint64 // archive holding the log from RedoLSN onward
}

// WriteBackupLabel publishes the label via tmp plus rename, so a crash
// never leaves a half-written one. The reader refuses what it cannot parse,
// and a torn label would be unreadable anyway.
func WriteBackupLabel(path string, l BackupLabel) error {
	buf := make([]byte, BackupLabelSize)
	copy(buf, backupStamp)
	binary.LittleEndian.PutUint32(buf[4:], backupVersion)
	binary.LittleEndian.PutUint64(buf[8:], uint64(l.RedoLSN))
	binary.LittleEndian.PutUint64(buf[16:], l.NextXid)
	binary.LittleEndian.PutUint64(buf[24:], l.ArchiveSeq)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadBackupLabel loads a label. Missing or corrupt files error: without a
// start point a restore must refuse, never guess.
func ReadBackupLabel(path string) (BackupLabel, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return BackupLabel{}, err
	}
	if len(buf) != BackupLabelSize || string(buf[:4]) != string(backupStamp) ||
		binary.LittleEndian.Uint32(buf[4:]) != backupVersion {
		return BackupLabel{}, fmt.Errorf("wal: %s is not a backup label", path)
	}
	return BackupLabel{
		RedoLSN:    LSN(binary.LittleEndian.Uint64(buf[8:])),
		NextXid:    binary.LittleEndian.Uint64(buf[16:]),
		ArchiveSeq: binary.LittleEndian.Uint64(buf[24:]),
	}, nil
}

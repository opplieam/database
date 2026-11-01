// Package backup takes base backups and rewinds a copy to a chosen
// moment. A backup is a directory: the data, commit-log, and control
// files as copied, plus a backup_label naming where replay starts.
package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"database/wal"
)

// Cluster names the paths that make up one running cluster. Named
// fields because Backup and Restore each take many paths of the same
// type, and two swapped strings compile cleanly and fail at runtime.
type Cluster struct {
	Data     string // heap file: the rows
	Clog     string // commit log: one mark per transaction
	Ctl      string // control file: last checkpoint and its REDO
	WAL      string // write-ahead log segment
	Archives string // archive directory: copies of the WAL, outside the cluster
}

// Backup writes a base backup into outDir.
//
// The order below is the lesson. Checkpoint first, so REDO names the
// log tip at or before every copied page: Checkpoint flushes dirty
// pages and captures the tip as REDO before appending its own record,
// so replay from REDO when the copied files are recovered reproduces
// any moment at or after the copy. Copies taken before the checkpoint
// would leave mid-copy changes in neither place: the pages predate
// them, and replay starts past them.
func Backup(c Cluster, outDir string, flusher wal.Flusher,
	seg *wal.Segment, nextXid, epoch uint64) error {

	// Refuse a finished backup before writing anything: the label is
	// the completion marker, published last, so a directory holding
	// one holds a complete backup. Overwriting would interleave one
	// backup's data file with another's label. A leftover from a
	// failed copy has no label, so a retry proceeds.
	if _, err := os.Stat(filepath.Join(outDir, "backup_label")); err == nil {
		return fmt.Errorf("backup: %s already holds a finished backup", outDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}

	// 1. Checkpoint: flushes pages and publishes the control file
	//    carrying checkpoint and REDO.
	_, redo, err := wal.Checkpoint(flusher, seg, c.Ctl, nextXid, epoch)
	if err != nil {
		return err
	}

	// 2. Archive moves the log out of the data directory, which a
	//    restore writes over. It flushes first, so the copy holds a
	//    complete log, and returns the sequence the label records.
	seq, err := seg.Archive(c.Archives)
	if err != nil {
		return err
	}

	// 3. Copy the cluster files under their own names. The control
	//    copy is never read: restore rewrites it from the label's
	//    REDO before recovery opens it. It is kept so the backup
	//    holds every file the cluster had.
	for _, p := range []string{c.Data, c.Clog, c.Ctl} {
		dst := filepath.Join(outDir, filepath.Base(p))
		if err := copyFile(p, dst); err != nil {
			return err
		}
	}

	// 4. The label last: from here on the directory holds a complete
	//    backup, and a second one refuses.
	return wal.WriteBackupLabel(filepath.Join(outDir, "backup_label"),
		wal.BackupLabel{RedoLSN: redo, NextXid: nextXid, ArchiveSeq: seq})
}

// copyFile copies src onto dst, byte for byte, and syncs the copy:
// a backup that vanishes with the process that made it is no backup.
// A partial file under dst is expected: the label, not this file,
// tells whether the directory holds a complete one.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	// One close for every exit out of the body below. It reports
	// only when nothing else failed, because the first error is the
	// one that explains the failure.
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, cerr := io.Copy(out, in); cerr != nil {
		return cerr
	}
	return out.Sync()
}

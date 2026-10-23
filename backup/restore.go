// Package backup takes base backups and rewinds a copy to a chosen
// moment. A backup is a directory: the data, commit-log, and control
// files as copied, plus a backup_label naming where replay starts.
package backup

import (
	"fmt"
	"path/filepath"
	"time"

	"database/buffer"
	"database/tx"
	"database/wal"
)

// Restore puts the cluster in c back to the backup's files plus replay
// up to target, and leaves it shut down: pages flushed, log cut at the
// stop, the way a machine looks just after a crash ends. Every handle
// opened here closes here, so the caller reopens into the restored
// cluster.
//
// Both refusal checks come first, before any copy touches the live
// paths: a refused restore leaves the damaged files damaged rather than
// half-replaced.
//
// The order below mirrors Backup in reverse. The label is read before
// anything else, because every later step follows it. The cluster files
// go back next; the control copy among them already names the backup's
// checkpoint, and the label's REDO is then published over it, so there
// is one start-point rule and it is the label's. The newest archive is
// last onto c.WAL: it is the log that outlived the backup, and copying
// it home is what supplies the replay window at all.
func Restore(c Cluster, backupDir string, target time.Time, mgr *tx.Manager) error {
	label, err := wal.ReadBackupLabel(filepath.Join(backupDir, "backup_label"))
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	// The label names the archive the log must come from. Archives are
	// never rewritten and the newest always covers the oldest, so a
	// newest behind the demand means the needed one is gone.
	archived, err := wal.LatestArchive(c.Archives)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if archived < label.ArchiveSeq {
		return fmt.Errorf("backup: newest archive is %d, the label demands %d",
			archived, label.ArchiveSeq)
	}

	for _, p := range []string{c.Data, c.Clog, c.Ctl} {
		src := filepath.Join(backupDir, filepath.Base(p))
		if err := copyFile(src, p); err != nil {
			return err
		}
	}
	// The label is authoritative and pg_control is its materialisation:
	// recovery keeps reading REDO from the control file it always has,
	// while the values come from the label. Checkpoint captures the
	// log tip before appending, so the two control fields hold one
	// value in every real file.
	if err := wal.WriteControl(c.Ctl, label.RedoLSN, label.RedoLSN); err != nil {
		return err
	}

	if err := copyFile(wal.ArchivePath(c.Archives, archived), c.WAL); err != nil {
		return err
	}
	seg, err := wal.Open(c.WAL)
	if err != nil {
		return err
	}
	defer seg.Close()
	// Opened after the copy: the handle loads the backup's marks, not
	// whatever sat in the damaged clog beforehand.
	clog, err := tx.OpenCommitLog(c.Clog)
	if err != nil {
		return err
	}
	defer clog.Close()

	pool := buffer.NewBufferPool(4)
	nextXid, epoch, stopLSN, err := pool.Recover(c.Data, seg, c.Ctl, clog, target)
	if err != nil {
		return err
	}
	// The restored counter pair comes from replay's return: the record at
	// REDO is the checkpoint record, whose payload holds the same pair the
	// label froze, so replay cannot return below the label's value and
	// needs no separate write for it.
	mgr.Restore(nextXid, epoch)

	// The cut is the rule replay's target cannot finish: the log still
	// holds commits past the target, and a later recovery without a
	// target would replay them, resurrecting the mistake. The archive
	// is never cut, so the same backup can restore another target.
	if err := seg.Truncate(stopLSN); err != nil {
		return err
	}
	return pool.FlushAll()
}

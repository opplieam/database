// Archive file layout:
//
//	byte-identical to the segment it copied: archiving copies, it
//	does not re-encode
package wal

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// archiveExt names archived logs. Sequence numbers are zero-padded so
// lexical order equals numeric order: newest is a directory-list question.
const archiveExt = ".wal"

// Archive copies the log into dir under the next sequence number and
// returns it: one past LatestArchive, so the first archive is 000001.
//
// Why copy at all: the log lives in the data directory, and restoring a
// base backup writes over that directory. The archive is the copy that
// survives the restore. PostgreSQL archives each full 16 MB segment as
// the archiver retires it; our segment never rolls over, so one archive
// is the whole log up to this moment.
//
// The flush comes first: an archive missing its last record is worse
// than no archive, because it looks complete.
func (s *Segment) Archive(dir string) (uint64, error) {
	latest, err := LatestArchive(dir)
	if err != nil {
		return 0, err
	}
	seq := latest + 1
	if err := s.Flush(); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}

	path := ArchivePath(dir, seq)
	// Create-only: os.O_EXCL makes the open fail when the name already
	// exists, so an archive is written once and never replaced.
	// Overwriting one would destroy the log an earlier backup depends on.
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}
	done := false
	defer func() {
		dst.Close()
		if !done {
			// A failed copy leaves a short file under a name that is
			// never reused. Remove it: a short archive reads as a
			// complete one.
			os.Remove(path)
		}
	}()

	// SectionReader pins the copy to the file's start. The write offset
	// sits past the magic after writeMagic, so a plain read would archive
	// the log without its magic and the archive would not open.
	src := io.NewSectionReader(s.file, 0, int64(s.end))
	if _, err := io.Copy(dst, src); err != nil {
		return 0, err
	}
	if err := dst.Sync(); err != nil {
		return 0, err
	}
	done = true
	return seq, nil
}

// LatestArchive returns the highest sequence number in dir, or 0 when
// dir is missing or holds no archives. A restore reads the newest
// archive, so "newest" has to be a cheap question to answer.
func LatestArchive(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var latest uint64
	for _, entry := range entries {
		seq, ok := parseArchiveName(entry.Name())
		if ok && seq > latest {
			latest = seq
		}
	}
	return latest, nil
}

// ArchivePath is the file name sequence seq gets inside dir.
func ArchivePath(dir string, seq uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%06d%s", seq, archiveExt))
}

// parseArchiveName recovers the sequence from an archive file name,
// reporting false for anything else in the directory: a stray file must
// not shift the numbering.
func parseArchiveName(name string) (uint64, bool) {
	if filepath.Ext(name) != archiveExt {
		return 0, false
	}
	seq, err := strconv.ParseUint(strings.TrimSuffix(name, archiveExt), 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

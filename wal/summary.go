// WAL summary layout, little-endian, 28 + 4n bytes:
//
//	[0-7]   magic: "SMRY" + version, rejects non-summary files
//	[8-15]  start_lsn: first LSN the covered stretch includes
//	[16-23] end_lsn: one past the last LSN the stretch includes
//	[24-27] page_count: data file length in pages when end_lsn was reached
//	[28..]  n page ids, ascending, one per changed page
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"database/storage"
)

// summaryStamp: fixed stamp plus format version, 8 bytes total, matching
// the segment and backup-label stamps.
var summaryStamp = []byte{'S', 'M', 'R', 'Y'}

const summaryVersion = 1

// summaryHeaderSize is magic, both LSNs, and the page count. Everything
// after it is n four-byte page ids.
const summaryHeaderSize = 28

// errRangeEnd stops the summarize walk at the range boundary. The walk
// swallows it into a clean stop rather than a failure, the same pattern
// the recovery target applies.
var errRangeEnd = errors.New("wal: summary range end")

// Summary is an index entry over the log, not more log: the pages the
// records between StartLSN and EndLSN touched, and the data file's
// length in pages when that stretch ended. Ranges chain end to start,
// so a reader reassembles any interval by unioning the files whose
// ranges cover it.
type Summary struct {
	StartLSN  LSN
	EndLSN    LSN
	PageCount uint32
	Pages     []uint32
}

// SummaryPath names a summary by the range it answers. The name is the
// address of the answer, so a reader globs ranges rather than opening
// files to discover what they cover.
func SummaryPath(dir string, from, to LSN) string {
	return filepath.Join(dir, fmt.Sprintf("%016x-%016x.summary", uint64(from), uint64(to)))
}

// WriteSummary publishes s to dir under its range name, tmp plus rename,
// so a crash never leaves a half-written index. The caller owns the
// directory, matching WriteBackupLabel.
func WriteSummary(dir string, s Summary) error {
	buf := make([]byte, summaryHeaderSize+4*len(s.Pages))
	copy(buf, summaryStamp)
	binary.LittleEndian.PutUint32(buf[4:], summaryVersion)
	binary.LittleEndian.PutUint64(buf[8:], uint64(s.StartLSN))
	binary.LittleEndian.PutUint64(buf[16:], uint64(s.EndLSN))
	binary.LittleEndian.PutUint32(buf[24:], s.PageCount)
	for i, page := range s.Pages {
		binary.LittleEndian.PutUint32(buf[summaryHeaderSize+4*i:], page)
	}
	tmp := SummaryPath(dir, s.StartLSN, s.EndLSN) + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, SummaryPath(dir, s.StartLSN, s.EndLSN))
}

// ReadSummary loads the summary at path. Missing or corrupt files
// return an error and a zero summary: an index read must never guess.
func ReadSummary(path string) (Summary, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, err
	}
	if len(buf) < summaryHeaderSize ||
		string(buf[:4]) != string(summaryStamp) ||
		binary.LittleEndian.Uint32(buf[4:]) != summaryVersion {
		return Summary{}, fmt.Errorf("wal: %s is not a WAL summary", path)
	}
	if (len(buf)-summaryHeaderSize)%4 != 0 {
		return Summary{}, fmt.Errorf("wal: %s page list misaligned", path)
	}
	pages := make([]uint32, (len(buf)-summaryHeaderSize)/4)
	for i := range pages {
		pages[i] = binary.LittleEndian.Uint32(buf[summaryHeaderSize+4*i:])
	}
	return Summary{
		StartLSN:  LSN(binary.LittleEndian.Uint64(buf[8:])),
		EndLSN:    LSN(binary.LittleEndian.Uint64(buf[16:])),
		PageCount: binary.LittleEndian.Uint32(buf[24:]),
		Pages:     pages,
	}, nil
}

// Summarize walks the log from from (inclusive) to to (exclusive) and
// collects the pages insert records name, deduplicated and ascending:
// the same page landing ten rows still enters the index once. Non-insert
// records name no page, and an insert payload that refuses to parse
// means the range cannot be summarized truthfully, so the whole call
// fails rather than writing a partial index. dataPath answers
// PageCount: the call follows Checkpoint, whose flush made the on-disk
// length the state at end of range, so the stat reads the truth rather
// than predicting it.
func Summarize(seg *Segment, dataPath string, from, to LSN) (Summary, error) {
	if to < from {
		return Summary{}, fmt.Errorf("wal: summary range [%d, %d) ends before it starts", uint64(from), uint64(to))
	}
	seen := make(map[uint32]bool)
	err := seg.Iterate(from, func(lsn LSN, rec *Record) error {
		if lsn >= to {
			return errRangeEnd
		}
		if rec.Type != RecordInsert {
			return nil
		}
		pageId, _, _, _, err := ParseInsert(rec.Payload)
		if err != nil {
			return err
		}
		seen[pageId] = true
		return nil
	})
	if err != nil && !errors.Is(err, errRangeEnd) {
		return Summary{}, err
	}
	pages := make([]uint32, 0, len(seen))
	for page := range seen {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	st, err := os.Stat(dataPath)
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		StartLSN:  from,
		EndLSN:    to,
		PageCount: uint32(st.Size() / storage.PageSize),
		Pages:     pages,
	}, nil
}

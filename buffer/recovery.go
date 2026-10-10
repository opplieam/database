package buffer

import (
	"fmt"

	"database/wal"
)

// Recover replays the WAL forward from the control file's REDO point,
// rebuilding pages that died dirty in memory. Single data file: insert
// payloads carry page ids, not file identity, so every record addresses
// dataPath (see plan follow-ups). Ends with FlushAll so recovered pages
// land on disk. Missing or corrupt control files refuse via ReadControl.
func (p *BufferPool) Recover(dataPath string, seg *wal.Segment, ctlPath string) error {
	_, redo, err := wal.ReadControl(ctlPath)
	if err != nil {
		return err
	}
	err = seg.Iterate(redo, func(lsn wal.LSN, rec *wal.Record) error {
		if rec.Type != wal.RecordInsert {
			return nil // checkpoint and commit markers: nothing to apply
		}
		pageId, slot, bitmap, row, err := wal.ParseInsert(rec.Payload)
		if err != nil {
			return fmt.Errorf("buffer: bad insert at LSN %d: %w", lsn, err)
		}
		tag := BufferTag{Path: dataPath, PageId: pageId}
		page, err := p.Get(tag)
		if err != nil {
			return err
		}
		if wal.LSN(page.Header.LSN) >= lsn {
			// Page already holds this change (flushed before the crash).
			return p.Unpin(tag)
		}
		if !page.AddRecord(row, bitmap) {
			_ = p.Unpin(tag)
			return fmt.Errorf("buffer: replay overflows page %d", pageId)
		}
		if got := page.Header.RecordCount - 1; got != slot {
			_ = p.Unpin(tag)
			return fmt.Errorf("buffer: replay landed slot %d, log says %d", got, slot)
		}
		page.Header.LSN = uint64(lsn)
		if err := p.MarkDirty(tag); err != nil {
			_ = p.Unpin(tag)
			return err
		}
		return p.Unpin(tag)
	})
	if err != nil {
		return err
	}
	return p.FlushAll()
}

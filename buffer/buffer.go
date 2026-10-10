// Package buffer
//
// This package implements a buffer pool: an in-memory shelf of page slots
// between executors and disk.
//
// ## What we implement
//
// A fixed-size pool with three read paths, clock-sweep eviction, dirty
// tracking, and manual flush.
//
// How it works:
//  1. Get(tag) checks the tag index (hit: pin and return)
//  2. On miss, take a free slot or evict a clock-sweep victim
//  3. Dirty victims are flushed before reuse
//  4. Writes only MarkDirty; bytes reach disk on eviction or FlushAll
//
// Benefits:
//   - Fast: Repeated reads become memory lookups
//   - Simple: No locks, the engine is single-threaded
//   - Measurable: Hit/miss/eviction counters feed the cost tracker
//
// Limitations:
//   - No durability: A crash loses dirty pages until WAL exists
//   - File opened per load/flush, not held open
//
// ## What we skip (not implemented)
//
// Ring buffer, async I/O, locks, checkpointer/background writer,
// local buffers.
//
// We skip them because:
//   - Too complex for a learning project at this stage
//   - Single-threaded engine has no contention or background processes
//   - Manual FlushAll suffices until WAL exists
package buffer

import (
	"errors"
	"fmt"

	"database/storage"
	"database/wal"
)

// maxUsageCount caps clock-sweep popularity, copied from PostgreSQL's
// 4-bit usage count field.
const maxUsageCount = 15

// BufferTag identifies a page: which file plus which page in it.
type BufferTag struct {
	Path   string
	PageId uint32
}

// Descriptor holds one slot's metadata: validity, dirtiness, pin count
// (current users), and usage count (clock-sweep popularity).
type Descriptor struct {
	Tag        BufferTag
	Valid      bool
	Dirty      bool
	PinCount   int
	UsageCount int
}

// slot pairs a descriptor with its cached page.
type slot struct {
	desc Descriptor
	page *storage.Page
}

// BufferPool is a fixed-size set of page slots with a tag index,
// a clock-sweep hand, and hit/miss/eviction counters.
type BufferPool struct {
	slots      []slot
	index      map[BufferTag]int
	nextVictim int
	walSeg     *wal.Segment // nil = no WAL attached, legacy behavior
	hits       int
	misses     int
	evictions  int
	flushes    int
}

// PoolStats reports cache behavior for later cost-tracker wiring.
type PoolStats struct {
	Hits      int
	Misses    int
	Evictions int
	Flushes   int
}

// NewBufferPool creates a pool with the given slot count (minimum 1).
func NewBufferPool(size int) *BufferPool {
	if size < 1 {
		size = 1
	}
	return &BufferPool{
		slots: make([]slot, size),
		index: make(map[BufferTag]int),
	}
}

// SetWAL attaches a WAL segment opened by the caller. Setup owns the
// file; the pool only borrows it. Pools without one behave as before.
func (p *BufferPool) SetWAL(seg *wal.Segment) { p.walSeg = seg }

// WAL returns the attached segment, or nil when detached.
func (p *BufferPool) WAL() *wal.Segment { return p.walSeg }

// Get returns the page for tag and pins it.
//
// Hit: tag found in index, bump pin and usage counts, return cached page.
// Miss: take a free slot or evict a clock-sweep victim (flushing it first
// if dirty), then load the page from disk.
//
// Example:
//
//	pool size 2, page 0 cached in slot 0, page 1 cached in slot 1
//	Get(page 0) -> hit, slot 0 pin 1->2, zero disk I/O
//	Get(page 2) -> miss, sweep evicts a victim, loads page 2 from disk
func (p *BufferPool) Get(tag BufferTag) (*storage.Page, error) {
	// Step 1: cache hit? Pin and return, zero disk I/O.
	if idx, ok := p.index[tag]; ok {
		s := &p.slots[idx]
		s.desc.PinCount++
		if s.desc.UsageCount < maxUsageCount {
			s.desc.UsageCount++
		}
		p.hits++
		return s.page, nil
	}

	p.misses++

	// Step 2: miss. Find a free slot or evict a victim.
	idx, err := p.findVictim()
	if err != nil {
		return nil, err
	}
	s := &p.slots[idx]

	// Step 3: victim held a live page? Flush dirty bytes, forget old tag.
	if s.desc.Valid {
		if s.desc.Dirty {
			if err := p.flushSlot(idx); err != nil {
				return nil, err
			}
		}
		delete(p.index, s.desc.Tag)
		p.evictions++
	}

	// Step 4: load the wanted page and stamp a fresh descriptor.
	page, err := loadPage(tag)
	if err != nil {
		return nil, err
	}

	s.desc = Descriptor{
		Tag:        tag,
		Valid:      true,
		Dirty:      false,
		PinCount:   1,
		UsageCount: 1,
	}
	s.page = page
	p.index[tag] = idx
	return page, nil
}

// Unpin releases a page previously returned by Get. Get and Unpin always
// pair up; a leaked pin makes the slot unevictable.
func (p *BufferPool) Unpin(tag BufferTag) error {
	idx, ok := p.index[tag]
	if !ok {
		return fmt.Errorf("buffer: unpin of unknown page %+v", tag)
	}
	s := &p.slots[idx]
	if s.desc.PinCount == 0 {
		return fmt.Errorf("buffer: unpin of unpinned page %+v", tag)
	}
	s.desc.PinCount--
	return nil
}

// MarkDirty flags a cached page as modified. The bytes reach disk on
// eviction or Flush, never here.
func (p *BufferPool) MarkDirty(tag BufferTag) error {
	idx, ok := p.index[tag]
	if !ok {
		return fmt.Errorf("buffer: dirty of unknown page %+v", tag)
	}
	p.slots[idx].desc.Dirty = true
	return nil
}

// Flush writes one dirty page to disk immediately.
func (p *BufferPool) Flush(tag BufferTag) error {
	idx, ok := p.index[tag]
	if !ok {
		return fmt.Errorf("buffer: flush of unknown page %+v", tag)
	}
	return p.flushSlot(idx)
}

// FlushAll writes every dirty page. This is the manual checkpoint:
// the only durability the pool offers until WAL exists.
//
// Example:
//
//	slots hold dirty pages 0 and 1 -> both written, both Dirty cleared
//	slots hold only clean pages -> no-op, zero disk I/O
func (p *BufferPool) FlushAll() error {
	for idx := range p.slots {
		if err := p.flushSlot(idx); err != nil {
			return err
		}
	}
	return nil
}

// Stats returns hit, miss, and eviction counters.
func (p *BufferPool) Stats() PoolStats {
	return PoolStats{
		Hits:      p.hits,
		Misses:    p.misses,
		Evictions: p.evictions,
		Flushes:   p.flushes,
	}
}

// PinnedCount returns the number of slots currently pinned. Used by tests
// to prove scans release every page they borrow.
func (p *BufferPool) PinnedCount() int {
	n := 0
	for i := range p.slots {
		if p.slots[i].desc.Valid && p.slots[i].desc.PinCount > 0 {
			n++
		}
	}
	return n
}

// findVictim runs clock sweep to pick a slot for reuse.
//
// Algorithm: walk slots in a circle from the hand, and per slot:
//   1. Free slot -> take it, no eviction.
//   2. Pinned slot -> skip, usage untouched.
//   3. Unpinned slot with usage above zero -> decay by one, move on.
//   4. Unpinned slot at zero -> victim.
//
// Example:
//   usages [2, 0], hand at 0 -> decay slot 0 to 1, take slot 1
//   all slots pinned -> error, caller must Unpin first
//
// The walk always ends: each idle slot needs at most 15 decays, so the
// loop bound covers every case including all-pinned.
func (p *BufferPool) findVictim() (int, error) {
	if len(p.slots) == 0 {
		return 0, errors.New("buffer: pool has no slots")
	}
	// Each unpinned slot needs at most maxUsageCount decays, so this
	// bound always terminates, including the all-pinned case.
	for checked := 0; checked < len(p.slots)*(maxUsageCount+1); checked++ {
		cand := &p.slots[p.nextVictim]
		// Free slot: take it, no eviction needed.
		if !cand.desc.Valid {
			idx := p.nextVictim
			p.nextVictim = (p.nextVictim + 1) % len(p.slots)
			return idx, nil
		}
		// Pinned slots are invisible to the sweep; only unpinned ones cool down.
		if cand.desc.PinCount == 0 {
			// First unpinned slot cooled to zero wins.
			if cand.desc.UsageCount == 0 {
				idx := p.nextVictim
				p.nextVictim = (p.nextVictim + 1) % len(p.slots)
				return idx, nil
			}
			cand.desc.UsageCount--
		}
		p.nextVictim = (p.nextVictim + 1) % len(p.slots)
	}
	return 0, errors.New("buffer: all slots pinned, no victim available")
}

// flushSlot writes the slot's page if dirty and valid, then clears dirty.
func (p *BufferPool) flushSlot(idx int) error {
	s := &p.slots[idx]
	if !s.desc.Valid || !s.desc.Dirty {
		return nil
	}
	// Write-ahead rule: the log describing this page must be durable
	// before the page itself. Eviction, Flush, and FlushAll all funnel
	// through here, so one call covers every page-write path.
	if p.walSeg != nil {
		if err := p.walSeg.Flush(); err != nil {
			return err
		}
	}
	f, err := storage.Open(s.desc.Tag.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.WritePage(s.desc.Tag.PageId, s.page); err != nil {
		return err
	}
	s.desc.Dirty = false
	p.flushes++
	return nil
}

// loadPage reads one page from disk. The file is opened and closed per
// call, matching how storage.File is used everywhere else today.
func loadPage(tag BufferTag) (*storage.Page, error) {
	f, err := storage.Open(tag.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadPage(tag.PageId)
}

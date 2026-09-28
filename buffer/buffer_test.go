package buffer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"database/storage"
)

// makeHeapFile writes one movie record per page for easy identification.
func makeHeapFile(t *testing.T, path string, titles []string) {
	t.Helper()
	f, err := storage.Create(path)
	require.NoError(t, err)
	defer f.Close()
	for i, title := range titles {
		page := storage.NewPage(uint32(i))
		enc := storage.EncodeMovieRecord(uint32(i+1), title, "Genre")
		require.True(t, page.AddRecord(enc, 0), "record must fit in empty page")
		require.NoError(t, f.AppendPage(page))
	}
}

// readTitle reads the title back through the pool for content checks.
func readTitle(t *testing.T, p *BufferPool, tag BufferTag) string {
	t.Helper()
	page, err := p.Get(tag)
	require.NoError(t, err)
	defer p.Unpin(tag)
	rec, err := page.GetRecord(0)
	require.NoError(t, err)
	_, title, _, err := storage.DecodeMovieRecord(rec)
	require.NoError(t, err)
	return title
}

func TestPoolMissThenHit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.data")
	makeHeapFile(t, path, []string{"Toy Story", "Jumanji"})

	p := NewBufferPool(2)
	tag := BufferTag{Path: path, PageId: 0}

	assert.Equal(t, "Toy Story", readTitle(t, p, tag)) // miss
	assert.Equal(t, "Toy Story", readTitle(t, p, tag)) // hit

	stats := p.Stats()
	assert.Equal(t, 1, stats.Misses)
	assert.Equal(t, 1, stats.Hits)
	assert.Equal(t, 0, stats.Evictions)
}

func TestClockSweep(t *testing.T) {
	tests := []struct {
		name      string
		pins      []int // pin count per slot, -1 means invalid slot
		usages    []int // usage count per slot
		wantVict  int
		wantErr   bool
		wantUsage []int // usage counts after the sweep
	}{
		{
			name:      "free slot wins immediately",
			pins:      []int{-1, 0},
			usages:    []int{0, 0},
			wantVict:  0,
			wantUsage: []int{0, 0},
		},
		{
			name:      "unpinned zero usage picked",
			pins:      []int{0, 0},
			usages:    []int{3, 0},
			wantVict:  1, // hand starts at 0, decays slot 0, takes slot 1
			wantUsage: []int{2, 0},
		},
		{
			name:      "pinned slots skipped",
			pins:      []int{2, 0},
			usages:    []int{0, 5},
			wantVict:  1, // slot 0 pinned despite zero usage
			wantUsage: []int{0, 0}, // decayed 5 -> 0 over rotations
		},
		{
			name:     "all pinned errors",
			pins:     []int{1, 3},
			usages:   []int{0, 0},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewBufferPool(len(tt.pins))
			for i := range tt.pins {
				if tt.pins[i] < 0 {
					continue
				}
				p.slots[i].desc = Descriptor{
					Tag:        BufferTag{PageId: uint32(i)},
					Valid:      true,
					PinCount:   tt.pins[i],
					UsageCount: tt.usages[i],
				}
				p.slots[i].page = storage.NewPage(uint32(i))
			}

			got, err := p.findVictim()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVict, got)
			for i, want := range tt.wantUsage {
				assert.Equal(t, want, p.slots[i].desc.UsageCount, "slot %d usage", i)
			}
		})
	}
}

func TestDirtyVictimFlushed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.data")
	makeHeapFile(t, path, []string{"Toy Story", "Jumanji"})

	p := NewBufferPool(1) // one slot forces eviction on second page
	tag0 := BufferTag{Path: path, PageId: 0}
	tag1 := BufferTag{Path: path, PageId: 1}

	// Load page 0, append a record, mark dirty, release.
	page0, err := p.Get(tag0)
	require.NoError(t, err)
	enc := storage.EncodeMovieRecord(99, "Added", "Genre")
	require.True(t, page0.AddRecord(enc, 0))
	require.NoError(t, p.MarkDirty(tag0))
	require.NoError(t, p.Unpin(tag0))

	// Loading page 1 evicts dirty page 0, which must reach disk first.
	assert.Equal(t, "Jumanji", readTitle(t, p, tag1))

	// Verify through a fresh file handle, bypassing the pool.
	f, err := storage.Open(path)
	require.NoError(t, err)
	defer f.Close()
	raw, err := f.ReadPage(0)
	require.NoError(t, err)
	rec, err := raw.GetRecord(1)
	require.NoError(t, err)
	_, title, _, err := storage.DecodeMovieRecord(rec)
	require.NoError(t, err)
	assert.Equal(t, "Added", title)

	assert.Equal(t, 1, p.Stats().Evictions)
}

func TestFlushAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.data")
	makeHeapFile(t, path, []string{"Toy Story", "Jumanji"})

	p := NewBufferPool(2)
	tag0 := BufferTag{Path: path, PageId: 0}
	tag1 := BufferTag{Path: path, PageId: 1}

	for _, tag := range []BufferTag{tag0, tag1} {
		_, err := p.Get(tag)
		require.NoError(t, err)
		require.NoError(t, p.MarkDirty(tag))
		require.NoError(t, p.Unpin(tag))
	}

	require.NoError(t, p.FlushAll())
	for _, tag := range []BufferTag{tag0, tag1} {
		assert.False(t, p.slots[p.index[tag]].desc.Dirty)
	}
}

func TestUnpinErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.data")
	makeHeapFile(t, path, []string{"Toy Story"})

	p := NewBufferPool(1)
	unknown := BufferTag{Path: path, PageId: 7}

	assert.Error(t, p.Unpin(unknown), "unpin of never-loaded page")

	tag := BufferTag{Path: path, PageId: 0}
	_, err := p.Get(tag)
	require.NoError(t, err)
	require.NoError(t, p.Unpin(tag))
	assert.Error(t, p.Unpin(tag), "double unpin")
	assert.Error(t, p.MarkDirty(unknown), "dirty of unknown page")
	assert.Error(t, p.Flush(unknown), "flush of unknown page")
}

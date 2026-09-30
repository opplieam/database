package vacuum

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"database/buffer"
	"database/storage"
)

// makeReporterHeap writes one record per page for predictable counting:
// each new page touched is exactly one miss.
func makeReporterHeap(t *testing.T, path string, n int) {
	t.Helper()
	f, err := storage.Create(path)
	require.NoError(t, err)
	defer f.Close()
	for i := 0; i < n; i++ {
		page := storage.NewPage(uint32(i))
		enc := storage.EncodeMovieRecord(uint32(i+1), "Title", "Genre")
		require.True(t, page.AddRecord(enc, 0))
		require.NoError(t, f.AppendPage(page))
	}
}

func TestCostReporterSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.data")
	makeReporterHeap(t, path, 2)

	pool := buffer.NewBufferPool(4)
	reporter := NewCostReporter(NewVacuumCostTracker(DefaultConfig()))

	// 2 Gets on page 0: 1 miss + 1 hit. Get page 1: 1 miss.
	// Dirty page 1 + flush: 1 flush.
	tag0 := buffer.BufferTag{Path: path, PageId: 0}
	tag1 := buffer.BufferTag{Path: path, PageId: 1}
	_, err := pool.Get(tag0)
	require.NoError(t, err)
	require.NoError(t, pool.Unpin(tag0))
	_, err = pool.Get(tag0)
	require.NoError(t, err)
	require.NoError(t, pool.Unpin(tag0))
	_, err = pool.Get(tag1)
	require.NoError(t, err)
	require.NoError(t, pool.MarkDirty(tag1))
	require.NoError(t, pool.Unpin(tag1))
	require.NoError(t, pool.Flush(tag1))

	reporter.Sync(pool)
	stats := reporter.tracker.Stats()
	// 2 misses x 10 + 1 hit x 1 + 1 flush x 20 = 41, under the 200 limit.
	assert.Equal(t, 41, stats.TotalCost)
	assert.Equal(t, 0, stats.TotalPauses)

	// Second Sync with no new activity charges nothing.
	reporter.Sync(pool)
	assert.Equal(t, 41, reporter.tracker.Stats().TotalCost)
}

func TestCostReporterThrottles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "throttle.data")
	makeReporterHeap(t, path, 3)

	pool := buffer.NewBufferPool(4)
	config := VacuumCostConfig{
		Delay:     time.Millisecond,
		Limit:     25,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}
	reporter := NewCostReporter(NewVacuumCostTracker(config))

	// 3 misses = cost 30, past the limit of 25: one pause must fire.
	for i := uint32(0); i < 3; i++ {
		tag := buffer.BufferTag{Path: path, PageId: i}
		_, err := pool.Get(tag)
		require.NoError(t, err)
		require.NoError(t, pool.Unpin(tag))
	}

	reporter.Sync(pool)
	stats := reporter.tracker.Stats()
	assert.Equal(t, 1, stats.TotalPauses)
	assert.Equal(t, 30, stats.TotalCost)
}

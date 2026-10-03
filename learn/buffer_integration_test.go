package learn

import (
	"os"
	"strings"
	"testing"

	"database/buffer"
	"database/executors"
	"database/storage"
	"database/tx"
	"database/vacuum"

	"github.com/stretchr/testify/suite"
)

type BufferSuite struct {
	suite.Suite
	clog     *tx.CommitLog
	clogFile string
	mgr      *tx.TxManager
}

func (s *BufferSuite) SetupSuite() {
	s.clogFile = "test_buffer_clog.data"
	var err error
	s.clog, err = tx.OpenCommitLog(s.clogFile)
	s.Require().NoError(err)
}

func (s *BufferSuite) SetupTest() {
	s.mgr = tx.NewTxManager()
}

func (s *BufferSuite) TearDownSuite() {
	s.clog.Close()
	os.Remove(s.clogFile)
}

// writePooled inserts every record through the pool in one transaction.
func (s *BufferSuite) writePooled(path string, pool *buffer.BufferPool, movies []storage.MovieRecord) {
	wtx := s.mgr.Begin(tx.RepeatableRead)
	ctx := &executors.TransactionContext{
		Tx:       wtx,
		Clog:     s.clog,
		Snapshot: wtx.Id(),
		XipList:  wtx.XipList(),
	}
	for _, m := range movies {
		ins := executors.NewPoolInsertMVCC(path, m, 0, ctx, pool)
		_, err := ins.Next()
		s.Require().NoError(err)
	}
	s.mgr.Commit(wtx)
}

// scanPooled reads every visible tuple through the given pool. Same reader,
// different memories: a pool that saw the writes serves dirty pages, while
// a fresh pool serves only what reached disk.
func (s *BufferSuite) scanPooled(path string, pool *buffer.BufferPool) []storage.Tuple {
	rtx := s.mgr.Begin(tx.RepeatableRead)
	rctx := &executors.TransactionContext{
		Tx:       rtx,
		Clog:     s.clog,
		Snapshot: rtx.Id(),
		XipList:  rtx.XipList(),
	}
	scan, err := executors.NewPoolHeapScanMVCC(path, rctx, pool)
	s.Require().NoError(err)
	defer scan.Close()
	result, err := executors.Run(scan)
	s.Require().NoError(err)
	return result
}

func (s *BufferSuite) TestPooledRoundTrip() {
	path := "test_buffer_roundtrip.data"
	defer os.Remove(path)
	pool := buffer.NewBufferPool(4)

	s.T().Logf("Step 1: Insert 2 records through the pool (memory only)")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Genre"},
		{MovieId: 2, Title: "Jumanji", Genres: "Genre"},
	})
	s.T().Logf("  - pool stats: %+v (misses loaded pages, pins all returned)", pool.Stats())

	s.T().Logf("Step 2: Scan through the same pool (dirty pages serve, disk still blank)")
	memory := s.scanPooled(path, pool)
	s.Require().Equal(2, len(memory))
	s.Equal("Toy Story", memory[0][1])
	s.Equal("Jumanji", memory[1][1])
	s.Equal(0, pool.PinnedCount())

	s.T().Logf("Step 3: FlushAll copies dirty pages to disk")
	s.Require().NoError(pool.FlushAll())

	s.T().Logf("Step 4: Scan through a fresh pool (every read misses to disk)")
	disk := s.scanPooled(path, buffer.NewBufferPool(4))
	s.Require().Equal(2, len(disk))
	s.Equal("Toy Story", disk[0][1])
	s.Equal("Jumanji", disk[1][1])
}

func (s *BufferSuite) TestScanUnderEviction() {
	path := "test_buffer_evict.data"
	defer os.Remove(path)

	s.T().Logf("Setup: 40 records at ~290 bytes each span 4 pages (titles truncate at 255 chars)")
	var movies []storage.MovieRecord
	for i := 1; i <= 40; i++ {
		movies = append(movies, storage.MovieRecord{
			MovieId: uint32(i),
			Title:   strings.Repeat("a", 1000),
			Genres:  "Genre",
		})
	}

	s.T().Logf("Step 1: Write through one 2-slot pool (placement evicts dirty victims)")
	pool := buffer.NewBufferPool(2)
	s.writePooled(path, pool, movies)
	s.T().Logf("  - pool stats after writes: %+v", pool.Stats())

	s.T().Logf("Step 2: Scan through the same pool (clean victims drop, ids must stay exact)")
	result := s.scanPooled(path, pool)
	s.Require().Equal(40, len(result))
	for i, r := range result {
		s.Equal(uint32(i+1), r[0])
	}
	s.T().Logf("  - pool stats after scan: %+v", pool.Stats())
	s.Greater(pool.Stats().Evictions, 0)
	s.Equal(0, pool.PinnedCount())
}

func (s *BufferSuite) TestCostWiring() {
	path := "test_buffer_cost.data"
	defer os.Remove(path)
	pool := buffer.NewBufferPool(4)

	s.T().Logf("Step 1: Insert 2 records, scan twice (second scan is all hits)")
	s.writePooled(path, pool, []storage.MovieRecord{
		{MovieId: 1, Title: "Toy Story", Genres: "Genre"},
		{MovieId: 2, Title: "Jumanji", Genres: "Genre"},
	})
	_ = s.scanPooled(path, pool)
	_ = s.scanPooled(path, pool)
	stats := pool.Stats()
	s.T().Logf("  - pool stats: %+v", stats)
	s.Greater(stats.Misses, 0)
	s.Greater(stats.Hits, 0)

	s.T().Logf("Step 2: Sync reporter, tracker total must equal counters times configured rates")
	tracker := vacuum.NewVacuumCostTracker(vacuum.DefaultConfig())
	vacuum.NewCostReporter(tracker).Sync(pool)
	s.T().Logf("  - tracker total: %d (want misses*10 + hits*1 + flushes*20)",
		tracker.Stats().TotalCost)
	s.Equal(
		stats.Misses*10+stats.Hits*1+stats.Flushes*20,
		tracker.Stats().TotalCost,
	)
}

func TestBufferSuite(t *testing.T) {
	suite.Run(t, new(BufferSuite))
}

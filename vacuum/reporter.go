package vacuum

import (
	"database/buffer"
)

// CostReporter bridges the buffer pool and the cost tracker.
//
// The pool counts real I/O (hits, misses, dirty flushes) without knowing
// costs. The tracker charges costs without knowing the pool. The reporter
// holds a tracker plus the last seen pool snapshot; Sync diffs today's
// counters and replays each new event into the tracker.
//
// Example: pool reports 5 new misses, 3 new hits, 1 new flush.
//
//	Sync replays 5 x AccountMiss + 3 x AccountHit + 1 x AccountDirty
//	tracker TotalCost grows by 50 + 3 + 20 = 73
type CostReporter struct {
	tracker *VacuumCostTracker
	last    buffer.PoolStats
}

// NewCostReporter creates a reporter feeding the given tracker.
func NewCostReporter(tracker *VacuumCostTracker) *CostReporter {
	return &CostReporter{tracker: tracker}
}

// Sync charges the tracker for pool activity since the last Sync.
func (r *CostReporter) Sync(pool *buffer.BufferPool) {
	cur := pool.Stats()
	for i := 0; i < cur.Hits-r.last.Hits; i++ {
		r.tracker.AccountHit()
	}
	for i := 0; i < cur.Misses-r.last.Misses; i++ {
		r.tracker.AccountMiss()
	}
	for i := 0; i < cur.Flushes-r.last.Flushes; i++ {
		r.tracker.AccountDirty()
	}
	r.last = cur
}

package vacuum

import "time"

// VacuumCostConfig holds configuration for cost-based throttling.
//
// PostgreSQL defaults:
//   - vacuum_cost_delay = 20ms
//   - vacuum_cost_limit = 200
//   - vacuum_cost_page_hit = 1
//   - vacuum_cost_page_miss = 10
//   - vacuum_cost_page_dirty = 20
type VacuumCostConfig struct {
	Delay     time.Duration // Sleep time when limit exceeded
	Limit     int           // Cost threshold before sleeping
	PageHit   int           // Cost for reading page from cache
	PageMiss  int           // Cost for reading page from disk
	PageDirty int           // Cost for writing dirty page to disk
}

// DefaultConfig returns PostgreSQL-like defaults.
func DefaultConfig() VacuumCostConfig {
	return VacuumCostConfig{
		Delay:     20 * time.Millisecond,
		Limit:     200,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}
}

// VacuumCostTracker tracks I/O cost and triggers throttling.
type VacuumCostTracker struct {
	config      VacuumCostConfig
	currentCost int
	totalCost   int
	totalPauses int
	totalSleep  time.Duration
}

// NewVacuumCostTracker creates a new cost tracker.
func NewVacuumCostTracker(config VacuumCostConfig) *VacuumCostTracker {
	return &VacuumCostTracker{
		config: config,
	}
}

// AccountHit accounts for reading a page from cache.
func (t *VacuumCostTracker) AccountHit() {
	t.addCost(t.config.PageHit)
}

// AccountMiss accounts for reading a page from disk.
func (t *VacuumCostTracker) AccountMiss() {
	t.addCost(t.config.PageMiss)
}

// AccountDirty accounts for writing a dirty page to disk.
func (t *VacuumCostTracker) AccountDirty() {
	t.addCost(t.config.PageDirty)
}

// addCost adds cost and checks for throttle.
func (t *VacuumCostTracker) addCost(cost int) {
	t.currentCost += cost
	t.totalCost += cost
	t.checkThrottle()
}

// checkThrottle sleeps if cost exceeds limit.
func (t *VacuumCostTracker) checkThrottle() {
	if t.currentCost >= t.config.Limit {
		time.Sleep(t.config.Delay)
		t.currentCost = 0
		t.totalPauses++
		t.totalSleep += t.config.Delay
	}
}

// Stats returns current statistics.
func (t *VacuumCostTracker) Stats() CostStats {
	return CostStats{
		CurrentCost: t.currentCost,
		TotalCost:   t.totalCost,
		TotalPauses: t.totalPauses,
		TotalSleep:  t.totalSleep,
	}
}

// CostStats holds cost tracking statistics.
type CostStats struct {
	CurrentCost int
	TotalCost   int
	TotalPauses int
	TotalSleep  time.Duration
}

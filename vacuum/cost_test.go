package vacuum

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCostTracker_NoThrottle(t *testing.T) {
	config := VacuumCostConfig{
		Delay:     10 * time.Millisecond,
		Limit:     100,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}

	tracker := NewVacuumCostTracker(config)

	// Simulate operations (cost < limit)
	tracker.AccountHit()   // cost = 1
	tracker.AccountMiss()  // cost = 11
	tracker.AccountDirty() // cost = 31

	stats := tracker.Stats()
	assert.Equal(t, 31, stats.CurrentCost)
	assert.Equal(t, 0, stats.TotalPauses)
}

func TestCostTracker_Throttle(t *testing.T) {
	config := VacuumCostConfig{
		Delay:     10 * time.Millisecond,
		Limit:     50,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}

	tracker := NewVacuumCostTracker(config)

	// Simulate operations (cost exceeds limit)
	tracker.AccountMiss()  // cost = 10
	tracker.AccountDirty() // cost = 30
	tracker.AccountDirty() // cost = 50 → THROTTLE!

	stats := tracker.Stats()
	assert.Equal(t, 0, stats.CurrentCost) // Reset
	assert.Equal(t, 1, stats.TotalPauses)
	assert.Equal(t, 10*time.Millisecond, stats.TotalSleep)
}

func TestCostTracker_MultiplePauses(t *testing.T) {
	config := VacuumCostConfig{
		Delay:     10 * time.Millisecond,
		Limit:     30,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}

	tracker := NewVacuumCostTracker(config)

	// Trigger multiple pauses
	tracker.AccountDirty() // cost = 20
	tracker.AccountDirty() // cost = 40 → THROTTLE (pause 1)
	tracker.AccountMiss()  // cost = 10
	tracker.AccountMiss()  // cost = 20
	tracker.AccountMiss()  // cost = 30 → THROTTLE (pause 2)

	stats := tracker.Stats()
	assert.Equal(t, 2, stats.TotalPauses)
	assert.Equal(t, 20*time.Millisecond, stats.TotalSleep)
}

func TestCostTracker_CacheHitVsMiss(t *testing.T) {
	config := VacuumCostConfig{
		Delay:     10 * time.Millisecond,
		Limit:     100,
		PageHit:   1,
		PageMiss:  10,
		PageDirty: 20,
	}

	tracker := NewVacuumCostTracker(config)

	// 10 cache hits = cost 10
	for i := 0; i < 10; i++ {
		tracker.AccountHit()
	}

	// 10 cache misses = cost 100
	for i := 0; i < 10; i++ {
		tracker.AccountMiss()
	}

	stats := tracker.Stats()
	assert.Equal(t, 110, stats.TotalCost) // 10 + 100
	assert.Equal(t, 1, stats.TotalPauses)  // Only once (at 100)
}

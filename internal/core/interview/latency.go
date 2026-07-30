package interview

import (
	"slices"
	"sync"
	"time"
)

// LatencyWindow records in-process Provider timings and exposes P95.
type LatencyWindow struct {
	mu      sync.Mutex
	samples []time.Duration
}

// Observe adds one non-negative latency sample.
func (window *LatencyWindow) Observe(value time.Duration) {
	if window == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	window.samples = append(window.samples, value)
}

// P95 returns the nearest-rank 95th percentile, or zero without samples.
func (window *LatencyWindow) P95() time.Duration {
	if window == nil {
		return 0
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	if len(window.samples) == 0 {
		return 0
	}
	values := slices.Clone(window.samples)
	slices.Sort(values)
	rank := (95*len(values) + 99) / 100
	return values[max(0, rank-1)]
}

// Count returns the number of observed Provider calls.
func (window *LatencyWindow) Count() int {
	if window == nil {
		return 0
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	return len(window.samples)
}

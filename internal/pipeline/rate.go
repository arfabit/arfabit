package pipeline

import (
	"fmt"
	"sync"
	"time"
)

// rateWindow is how far back a speed is averaged over.
//
// Short enough that the number reacts when the drive changes gear, long enough
// that it does not jitter with every update.
const rateWindow = 5 * time.Second

// rateTracker measures how fast something is being read or written.
//
// Averaged over a window rather than taken between two updates: a disc's read
// speed varies constantly, and an instantaneous figure flickers too much to
// read.
type rateTracker struct {
	mu      sync.Mutex
	samples []rateSample
}

type rateSample struct {
	at    time.Time
	bytes int64
}

// Observe records progress and returns the current rate in bytes per second.
//
// Returns zero until there is enough to measure, which is honest: a rate from
// two samples a tenth of a second apart is noise.
func (t *rateTracker) Observe(bytes int64) float64 {
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.samples = append(t.samples, rateSample{at: now, bytes: bytes})

	// Drop anything older than the window, keeping one sample beyond it so
	// there is always something to measure against.
	cutoff := now.Add(-rateWindow)
	for len(t.samples) > 2 && t.samples[1].at.Before(cutoff) {
		t.samples = t.samples[1:]
	}

	oldest := t.samples[0]
	elapsed := now.Sub(oldest.at).Seconds()
	if elapsed < 1 {
		return 0
	}

	moved := bytes - oldest.bytes
	if moved <= 0 {
		return 0
	}
	return float64(moved) / elapsed
}

// HumanRate renders a speed the way a person reads it.
func HumanRate(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f MB/s", bytesPerSecond/1_000_000)
}

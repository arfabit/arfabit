package pipeline

import (
	"testing"
	"time"
)

// A rate from two samples a tenth of a second apart is noise, and saying
// nothing is more honest than saying that.
func TestRateSaysNothingUntilItKnows(t *testing.T) {
	var tracker rateTracker

	if got := tracker.Observe(0); got != 0 {
		t.Errorf("a rate was reported from one sample: %v", got)
	}
	if got := tracker.Observe(1_000_000); got != 0 {
		t.Errorf("a rate was reported from two samples an instant apart: %v", got)
	}
}

func TestRateMeasuresOverTheWindow(t *testing.T) {
	var tracker rateTracker

	// Backdate the first sample so the window has something to measure.
	tracker.samples = []rateSample{{at: time.Now().Add(-4 * time.Second), bytes: 0}}

	got := tracker.Observe(40_000_000)
	if got < 9_000_000 || got > 11_000_000 {
		t.Errorf("rate = %.0f bytes/s, want about 10 million", got)
	}
}

// Progress that has not moved is not a speed of zero to be reported as a
// figure; it is nothing worth saying.
func TestRateIgnoresStandingStill(t *testing.T) {
	var tracker rateTracker
	tracker.samples = []rateSample{{at: time.Now().Add(-4 * time.Second), bytes: 5_000_000}}

	if got := tracker.Observe(5_000_000); got != 0 {
		t.Errorf("standing still reported %v", got)
	}
}

func TestHumanRate(t *testing.T) {
	if got := HumanRate(2_600_000); got != "2.6 MB/s" {
		t.Errorf("HumanRate = %q, want 2.6 MB/s", got)
	}
	if got := HumanRate(0); got != "" {
		t.Errorf("an unknown rate rendered as %q, want nothing", got)
	}
}

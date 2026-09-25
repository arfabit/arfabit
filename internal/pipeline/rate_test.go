package pipeline

import (
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/store"
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

// Each point on the read-speed graph is the average over its half minute,
// not the speed at one moment of it.
func TestReadSpeedIsAveraged(t *testing.T) {
	job := &Job{Job: &store.Job{}}
	speed := speedSampler{job: job}

	speed.observe(10*time.Second, 0)             // starts the first stretch
	speed.observe(20*time.Second, 900_000_000)   // a burst, too soon to count
	speed.observe(40*time.Second, 960_000_000)   // 960 MB over 30 s
	speed.observe(55*time.Second, 1_000_000_000) // too soon again
	speed.observe(70*time.Second, 1_140_000_000) // 180 MB over 30 s
	speed.observe(80*time.Second, 5)             // counting started again
	speed.observe(100*time.Second, 300_000_000)  // only 20 s into the new stretch
	speed.observe(110*time.Second, 600_000_005)  // 600 MB over 30 s

	want := []store.SpeedSample{
		{Seconds: 40, MBPerSecond: 32},
		{Seconds: 70, MBPerSecond: 6},
		{Seconds: 110, MBPerSecond: 20},
	}
	if len(job.ReadSpeed) != len(want) {
		t.Fatalf("kept %v, want %v", job.ReadSpeed, want)
	}
	for i := range want {
		if job.ReadSpeed[i] != want[i] {
			t.Errorf("point %d = %v, want %v", i, job.ReadSpeed[i], want[i])
		}
	}
}

// A drive's speed is kept under its name, which stays the same from disc to
// disc, and under its device path only when there is no name.
func TestDriveKeyPrefersTheName(t *testing.T) {
	if got := DriveKey("BD-RE BU40N", "/dev/rdisk8"); got != "BD-RE BU40N" {
		t.Errorf("DriveKey = %q, want the name", got)
	}
	if got := DriveKey("", "/dev/rdisk8"); got != "/dev/rdisk8" {
		t.Errorf("DriveKey = %q, want the device path", got)
	}
}

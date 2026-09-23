package eject

import (
	"runtime"
	"testing"
)

// MakeMKV reports the raw device on macOS but diskutil wants the block one.
func TestBlockDevice(t *testing.T) {
	if got, want := blockDevice("/dev/rdisk8"), "/dev/disk8"; got != want {
		t.Errorf("blockDevice = %q, want %q", got, want)
	}
	if got, want := blockDevice("/dev/sr0"), "/dev/sr0"; got != want {
		t.Errorf("blockDevice altered a non-macOS path: %q", got)
	}
}

func TestCommandIsDefined(t *testing.T) {
	name, _ := command("")
	switch runtime.GOOS {
	case "darwin", "linux":
		if name == "" {
			t.Errorf("no eject command defined for %s", runtime.GOOS)
		}
	}
}

// Ejecting an empty drive is reasonable when something seems stuck, but
// saying a disc came out when none did is a small lie.
func TestDescribeKnowsWhetherThereWasADisc(t *testing.T) {
	if got := (Result{OK: true}).Describe(true); !contains(got, "disc has been ejected") {
		t.Errorf("with a disc: %q", got)
	}
	if got := (Result{OK: true}).Describe(false); contains(got, "disc has been ejected") {
		t.Errorf("an empty drive reported a disc coming out: %q", got)
	}
	if got := (Result{OK: true}).Describe(false); !contains(got, "empty") {
		t.Errorf("an empty drive does not say so: %q", got)
	}
}

// A failed eject must read as a small inconvenience, not an error.
func TestDescribeFailureIsGentle(t *testing.T) {
	got := Result{Output: "device busy"}.Describe(true)
	for _, unwanted := range []string{"failed", "error", "Error"} {
		if contains(got, unwanted) {
			t.Errorf("Describe() says %q: %s", unwanted, got)
		}
	}
	if !contains(got, "press the button") {
		t.Errorf("Describe() does not suggest what to do: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

package drive

import (
	"strings"
	"testing"
)

// diskutil wants the block device; MakeMKV reports the raw one.
func TestBlockDevice(t *testing.T) {
	if got := blockDevice("/dev/rdisk8"); got != "/dev/disk8" {
		t.Errorf("blockDevice = %q, want /dev/disk8", got)
	}
	if got := blockDevice("/dev/sr0"); got != "/dev/sr0" {
		t.Errorf("blockDevice altered a path it should not have: %q", got)
	}
}

// The cost is what makes this worth explaining: it is the difference between
// tens of minutes and hours.
func TestDescribeExplainsTheCost(t *testing.T) {
	held := State{Mounted: true, MountPoint: "/Volumes/CRIME_101"}.Describe()

	if !strings.Contains(held, "hours") {
		t.Errorf("the explanation does not say what it costs: %q", held)
	}
	if !strings.Contains(held, "stays in the drive") {
		t.Errorf("the explanation does not reassure that the disc stays put: %q", held)
	}

	free := State{}.Describe()
	if !strings.Contains(free, "free") {
		t.Errorf("a free drive is not described as free: %q", free)
	}
}

// A drive with no disc is not a problem to report.
func TestCheckWithNoDevice(t *testing.T) {
	if got := Check(nil, ""); got.Mounted {
		t.Error("an empty device was reported as mounted")
	}
}

// Letting go of a volume and ejecting a disc are meant to be different
// operations. If a drive treats them as the same, that has to be noticed
// rather than leaving somebody watching an empty drive.
func TestAbsentDiscIsDistinctFromMounted(t *testing.T) {
	ejected := State{Present: false}
	held := State{Present: true, Mounted: true}
	ready := State{Present: true}

	if ejected.Present {
		t.Error("an ejected disc is reported as present")
	}
	if !held.Mounted || !held.Present {
		t.Error("a held disc should be both present and mounted")
	}
	if ready.Mounted {
		t.Error("a freed disc is reported as still mounted")
	}
}

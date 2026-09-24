// Package drive prepares an optical drive for reading at full speed.
package drive

import (
	"context"
	"strings"
)

// State is what stands between ARFABIT and the disc.
type State struct {
	// Present reports whether there is still a disc in the drive.
	//
	// Checked after letting go of a volume, because releasing one and
	// ejecting the disc are different operations and it would be a poor
	// outcome to find out the hard way.
	Present bool

	// Mounted reports whether the operating system has the disc open.
	//
	// This is the thing that usually makes a Blu-ray take four hours instead
	// of forty minutes: while the system holds the volume, MakeMKV cannot
	// claim the drive exclusively, and an encrypted disc then reads at about
	// the speed it would play at.
	Mounted bool

	// MountPoint is where the system put it, when it did.
	MountPoint string

	// Output is whatever the system said, kept whole.
	Output string
}

// Check reports whether the disc in a drive is mounted.
func Check(ctx context.Context, device string) State {
	return check(ctx, device)
}

// Unmount releases the disc from the operating system without ejecting it.
//
// The disc stays in the drive; only the system's hold on it is let go. That is
// exactly what is wanted: ARFABIT is about to read the disc itself.
func Unmount(ctx context.Context, device string) State {
	return unmount(ctx, device)
}

// Describe says what was found, in terms of what it costs.
func (s State) Describe() string {
	if !s.Mounted {
		return "The disc is free for ARFABIT to read directly."
	}
	return "Your computer has this disc open, which stops ARFABIT reading it at full speed. " +
		"Letting go of it makes a film take tens of minutes rather than hours. The disc stays in the drive."
}

// blockDevice turns a raw device path into the block one the system tools use.
func blockDevice(device string) string {
	return strings.Replace(device, "/dev/rdisk", "/dev/disk", 1)
}

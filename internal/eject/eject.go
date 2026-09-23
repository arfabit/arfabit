// Package eject opens the optical drive tray.
package eject

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Result describes what happened.
//
// A failed eject is a minor inconvenience, never a failed job: by the time
// ARFABIT ejects, the movie is already delivered. The UI says the disc did not
// come out and suggests the button on the drive, rather than reporting a
// failure (§15).
type Result struct {
	OK bool

	// Output is whatever the platform command said, kept verbatim.
	Output string
}

// Eject opens the tray of the drive at the given device path.
//
// The device may be empty, in which case the platform's default drive is used,
// which is the right behaviour on a machine with one drive.
func Eject(ctx context.Context, device string) Result {
	name, args := command(device)
	if name == "" {
		return Result{Output: "ARFABIT does not know how to open the drive on this system."}
	}

	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))

	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return Result{Output: text}
	}
	return Result{OK: true, Output: text}
}

// blockDevice converts a raw device path to its block equivalent.
//
// MakeMKV reports /dev/rdisk8 on macOS, but diskutil wants /dev/disk8. Passing
// the raw path fails with an unhelpful message.
func blockDevice(device string) string {
	return strings.Replace(device, "/dev/rdisk", "/dev/disk", 1)
}

// describe renders a short explanation of a failed eject for the UI.
func (r Result) Describe() string {
	if r.OK {
		return "The disc has been ejected."
	}
	return fmt.Sprintf("The disc did not come out. You can press the button on the drive. (%s)", r.Output)
}

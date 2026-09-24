//go:build darwin

package drive

import (
	"context"
	"os/exec"
	"strings"
)

// check asks diskutil whether the disc is mounted.
func check(ctx context.Context, device string) State {
	if device == "" {
		return State{}
	}

	out, err := exec.CommandContext(ctx, "diskutil", "info", blockDevice(device)).CombinedOutput()
	text := string(out)
	if err != nil {
		return State{Output: strings.TrimSpace(text)}
	}

	// diskutil answering at all means there is something in the drive.
	state := State{Present: true, Output: strings.TrimSpace(text)}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		switch key {
		case "Mounted":
			state.Mounted = strings.EqualFold(value, "Yes")
		case "Mount Point":
			state.MountPoint = value
		}
	}
	return state
}

// unmount lets go of the volume while leaving the disc in the drive.
//
// unmountDisk rather than eject: the disc must stay put, because reading it is
// the whole point.
func unmount(ctx context.Context, device string) State {
	if device == "" {
		return State{}
	}

	// unmountDisk, never eject: the disc has to stay in the drive, because
	// reading it is the entire point.
	out, err := exec.CommandContext(ctx, "diskutil", "unmountDisk", blockDevice(device)).CombinedOutput()
	if err != nil {
		return State{Present: true, Mounted: true, Output: strings.TrimSpace(string(out))}
	}

	return check(ctx, device)
}

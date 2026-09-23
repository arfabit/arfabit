// Package autostart makes ARFABIT start when the computer does.
//
// This is a toggle in the web interface rather than a page of instructions,
// because writing a launchd plist by hand is exactly the kind of thing this
// project exists to spare people.
package autostart

import (
	"fmt"
	"os"
)

// Status is whether ARFABIT is set to start on its own.
type Status struct {
	Enabled bool `json:"enabled"`

	// Path is the file that makes it happen, shown so it is never a mystery.
	Path string `json:"path"`

	// Mechanism names what the system calls this, for anyone who wants to look
	// it up.
	Mechanism string `json:"mechanism"`
}

// Enable installs the startup entry.
func Enable() (Status, error) {
	exe, err := os.Executable()
	if err != nil {
		return Status{}, fmt.Errorf("ARFABIT could not work out where it is installed: %w", err)
	}
	return enable(exe)
}

// Disable removes it. Nothing else is touched.
func Disable() (Status, error) {
	return disable()
}

// Current reports whether it is installed.
func Current() Status {
	return current()
}

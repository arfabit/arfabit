// Package restart starts ARFABIT again in place.
//
// Restarting is how most small problems get cleared, so it belongs in the web
// page rather than in a terminal the user may not have open.
package restart

import (
	"fmt"
	"os"
)

// Exec replaces this process with a fresh copy of ARFABIT, using the same
// arguments and environment.
//
// It does not return when it succeeds.
func Exec() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("ARFABIT could not find its own program file: %w", err)
	}
	return exec(exe, os.Args, os.Environ())
}

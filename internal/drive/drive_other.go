//go:build !darwin

package drive

import "context"

// check reports nothing on systems where this does not arise.
//
// Windows and Linux do not hold an optical drive in the way macOS does, so
// there is nothing to find and nothing to release.
func check(_ context.Context, _ string) State { return State{} }

func unmount(_ context.Context, _ string) State { return State{} }

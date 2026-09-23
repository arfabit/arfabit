//go:build linux

package eject

// command returns the Linux eject invocation.
//
// The eject utility is not installed everywhere; when it is missing the error
// says so plainly rather than silently doing nothing.
func command(device string) (string, []string) {
	if device == "" {
		return "eject", nil
	}
	return "eject", []string{device}
}

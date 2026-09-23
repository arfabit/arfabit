//go:build darwin

package eject

// command returns the macOS eject invocation.
//
// drutil handles the common single-drive case. With a specific device,
// diskutil is used instead because drutil addresses drives by its own index
// rather than by device path.
func command(device string) (string, []string) {
	if device == "" {
		return "drutil", []string{"eject"}
	}
	return "diskutil", []string{"eject", blockDevice(device)}
}

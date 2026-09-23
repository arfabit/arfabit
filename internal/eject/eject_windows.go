//go:build windows

package eject

import "fmt"

// command returns the Windows eject invocation.
//
// Windows ships no eject utility, so this goes through the Shell.Application
// COM object, which is what Explorer itself uses. The drive is addressed by
// letter; a device path is converted to one.
func command(device string) (string, []string) {
	letter := driveLetter(device)
	if letter == "" {
		return "", nil
	}

	script := fmt.Sprintf(
		`(New-Object -comObject Shell.Application).Namespace(17).ParseName("%s:").InvokeVerb("Eject")`,
		letter,
	)
	return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", script}
}

// driveLetter extracts a drive letter from whatever the backend reported.
func driveLetter(device string) string {
	for i := 0; i < len(device); i++ {
		c := device[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			if i+1 < len(device) && device[i+1] == ':' {
				return string(c)
			}
		}
	}
	return ""
}

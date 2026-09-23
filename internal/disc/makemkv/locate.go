package makemkv

import (
	"os"
	"os/exec"
	"runtime"
)

// candidatePaths lists where makemkvcon is normally found, per platform.
//
// MakeMKV's installers do not add it to PATH on macOS or Windows, so looking
// only at PATH reports "not installed" for most users who have in fact
// installed it. Doctor searches these before telling anyone anything is wrong.
func candidatePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/MakeMKV.app/Contents/MacOS/makemkvcon",
			"/opt/homebrew/bin/makemkvcon",
			"/usr/local/bin/makemkvcon",
		}
	case "windows":
		return []string{
			`C:\Program Files (x86)\MakeMKV\makemkvcon64.exe`,
			`C:\Program Files (x86)\MakeMKV\makemkvcon.exe`,
			`C:\Program Files\MakeMKV\makemkvcon64.exe`,
			`C:\Program Files\MakeMKV\makemkvcon.exe`,
		}
	default:
		return []string{
			"/usr/bin/makemkvcon",
			"/usr/local/bin/makemkvcon",
		}
	}
}

// Locate finds makemkvcon, preferring PATH and falling back to the usual
// install locations.
//
// Returns ErrNotInstalled when nothing is found, so Doctor can tell the user
// how to install it rather than surfacing an exec error.
func Locate() (string, error) {
	if p, err := exec.LookPath("makemkvcon"); err == nil {
		return p, nil
	}
	for _, p := range candidatePaths() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", ErrNotInstalled
}

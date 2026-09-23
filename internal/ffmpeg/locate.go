package ffmpeg

import (
	"errors"
	"runtime"
)

// ErrNotInstalled means ffmpeg or ffprobe could not be found.
var ErrNotInstalled = errors.New("not found")

const defaultShellExt = ""

// candidateDirs lists where ffmpeg is normally installed.
//
// As with makemkvcon, PATH alone is not enough: people install ffmpeg by
// unzipping it somewhere, and telling them it is missing when it is not is
// the kind of small wrongness that wastes an afternoon.
func candidateDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	case "windows":
		return []string{
			`C:\Program Files\ffmpeg\bin`,
			`C:\ffmpeg\bin`,
		}
	default:
		return []string{"/usr/bin", "/usr/local/bin", "/snap/bin"}
	}
}

//go:build !windows

package pipeline

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// freeBytes reports free space on the volume holding a path.
//
// The target directory may not exist yet, so the search walks up to the
// nearest parent that does.
func freeBytes(path string) (int64, error) {
	for dir := path; ; {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(dir, &stat); err == nil {
			return int64(stat.Bavail) * int64(stat.Bsize), nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, fmt.Errorf("could not check free space on %s", path)
		}
		dir = parent
	}
}

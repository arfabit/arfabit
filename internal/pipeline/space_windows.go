//go:build windows

package pipeline

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

// freeBytes reports free space on the volume holding a path.
//
// Windows has no statfs, so this calls GetDiskFreeSpaceExW. As elsewhere, the
// target directory may not exist yet, so the search walks up to the nearest
// parent that does.
func freeBytes(path string) (int64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	for dir := path; ; {
		utf16, err := syscall.UTF16PtrFromString(dir)
		if err == nil {
			var freeToCaller, total, free uint64
			r, _, _ := getDiskFreeSpaceEx.Call(
				uintptr(unsafe.Pointer(utf16)),
				uintptr(unsafe.Pointer(&freeToCaller)),
				uintptr(unsafe.Pointer(&total)),
				uintptr(unsafe.Pointer(&free)),
			)
			if r != 0 {
				return int64(freeToCaller), nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, fmt.Errorf("could not check free space on %s", path)
		}
		dir = parent
	}
}

package doctor

import "syscall"

// makemkvKey is where MakeMKV keeps its settings on Windows. It writes no
// settings.conf there, so looking for one says it was never opened.
const makemkvKey = `Software\MakeMKV`

// readMakeMKVSettings reads MakeMKV's registry key.
func readMakeMKVSettings() (makemkvSettings, error) {
	s := makemkvSettings{where: `HKEY_CURRENT_USER\` + makemkvKey}

	var k syscall.Handle
	err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, syscall.StringToUTF16Ptr(makemkvKey), 0, syscall.KEY_READ, &k)
	if err != nil {
		return s, nil
	}
	defer syscall.RegCloseKey(k)
	s.opened = true

	// Only the size is asked for. More than a terminating NUL means a key.
	var n uint32
	err = syscall.RegQueryValueEx(k, syscall.StringToUTF16Ptr("app_Key"), nil, nil, nil, &n)
	s.hasKey = err == nil && n > 2
	return s, nil
}

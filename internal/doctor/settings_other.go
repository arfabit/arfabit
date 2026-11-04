//go:build !windows

package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// readMakeMKVSettings reads settings.conf, where MakeMKV keeps its settings
// everywhere but Windows.
func readMakeMKVSettings() (makemkvSettings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return makemkvSettings{}, err
	}
	path := filepath.Join(home, ".MakeMKV", "settings.conf")
	if runtime.GOOS == "darwin" {
		path = filepath.Join(home, "Library", "MakeMKV", "settings.conf")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return makemkvSettings{where: path}, nil
	}
	return makemkvSettings{
		where:  path,
		opened: true,
		hasKey: strings.Contains(string(data), "app_Key"),
	}, nil
}

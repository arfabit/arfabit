package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ShellSettings are what ARFABIT's app follows on macOS and Windows
// (docs/RELEASE.md §3, §4). They belong to this computer, so they are kept in
// the node's own folder. The page and the app's menu change them here; the app
// applies them.
type ShellSettings struct {
	// ShowIcon puts ARFABIT in the menu bar, or the notification area.
	ShowIcon bool `json:"show_icon"`

	// StartAtLogin has the app start when the person logs in.
	StartAtLogin bool `json:"start_at_login"`
}

// DefaultShellSettings are a new computer's: the icon shown, and nothing
// started at login until the person asks for it.
var DefaultShellSettings = ShellSettings{ShowIcon: true}

var shellMu sync.Mutex

func (s *Store) shellPath() string { return filepath.Join(s.nodeDir(), "shell.json") }

// ShellSettings returns this computer's settings for the app.
func (s *Store) ShellSettings() ShellSettings {
	shellMu.Lock()
	defer shellMu.Unlock()
	return s.readShellSettings()
}

func (s *Store) readShellSettings() ShellSettings {
	// Anything missing from the file keeps its default.
	settings := DefaultShellSettings
	if data, err := os.ReadFile(s.shellPath()); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	return settings
}

// ChangeShellSettings applies change to the settings and saves them.
func (s *Store) ChangeShellSettings(change func(*ShellSettings)) (ShellSettings, error) {
	shellMu.Lock()
	defer shellMu.Unlock()

	settings := s.readShellSettings()
	change(&settings)

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return settings, err
	}
	if err := os.MkdirAll(s.nodeDir(), 0o755); err != nil {
		return settings, err
	}
	return settings, writeAtomic(s.shellPath(), append(data, '\n'))
}

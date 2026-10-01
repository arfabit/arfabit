package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// UpdateSettings say how this computer takes new versions of ARFABIT
// (docs/RELEASE.md §6). They belong to this computer, so they are kept in the
// node's own folder.
type UpdateSettings struct {
	// CheckDaily asks once a day whether there is a new version. It is the
	// only contact ARFABIT makes that the person did not ask for.
	CheckDaily bool `json:"check_daily"`

	// InstallByThemselves installs a new version once nothing is running.
	// Off, it waits for Update and restart.
	InstallByThemselves bool `json:"install_by_themselves"`
}

// DefaultUpdateSettings are a new computer's: both on.
var DefaultUpdateSettings = UpdateSettings{CheckDaily: true, InstallByThemselves: true}

var updatesMu sync.Mutex

func (s *Store) updatesPath() string { return filepath.Join(s.nodeDir(), "updates.json") }

// UpdateSettings returns this computer's settings for updates.
func (s *Store) UpdateSettings() UpdateSettings {
	updatesMu.Lock()
	defer updatesMu.Unlock()
	return s.readUpdateSettings()
}

func (s *Store) readUpdateSettings() UpdateSettings {
	// Anything missing from the file keeps its default.
	settings := DefaultUpdateSettings
	if data, err := os.ReadFile(s.updatesPath()); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	return settings
}

// ChangeUpdateSettings applies change to the settings and saves them.
func (s *Store) ChangeUpdateSettings(change func(*UpdateSettings)) (UpdateSettings, error) {
	updatesMu.Lock()
	defer updatesMu.Unlock()

	settings := s.readUpdateSettings()
	change(&settings)

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return settings, err
	}
	return settings, writeAtomic(s.updatesPath(), append(data, '\n'))
}

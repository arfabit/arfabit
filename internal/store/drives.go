package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// What a drive does when a disc goes in (§14). It belongs to this machine's
// drive, so it is kept in the node's own folder, by the drive's own name,
// which stays the same from disc to disc.
const (
	// WhenNothing waits for somebody to press Plan.
	WhenNothing = "nothing"

	// WhenCopy reads the disc and starts copying it, and reading its
	// subtitles, straight away. No film is made unless somebody asks.
	WhenCopy = "copy"

	// WhenBlueprint does the same and makes a film too, from a blueprint.
	WhenBlueprint = "blueprint"
)

// DriveSetting is what one drive does when a disc goes in.
type DriveSetting struct {
	When string `json:"when"`

	// Blueprint names the blueprint a film is made from, with WhenBlueprint.
	// Empty means the defaults.
	Blueprint string `json:"blueprint,omitempty"`
}

var drivesMu sync.Mutex

func (s *Store) drivesPath() string { return filepath.Join(s.nodeDir(), "drives.json") }

// DriveSettings returns what each drive does when a disc goes in, by the
// drive's name. A drive not listed does nothing.
func (s *Store) DriveSettings() map[string]DriveSetting {
	drivesMu.Lock()
	defer drivesMu.Unlock()
	return s.readDriveSettings()
}

func (s *Store) readDriveSettings() map[string]DriveSetting {
	settings := map[string]DriveSetting{}
	if data, err := os.ReadFile(s.drivesPath()); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	return settings
}

// SaveDriveSetting records what one drive does when a disc goes in.
func (s *Store) SaveDriveSetting(drive string, setting DriveSetting) error {
	drivesMu.Lock()
	defer drivesMu.Unlock()

	settings := s.readDriveSettings()
	if setting.When == WhenNothing || setting.When == "" {
		delete(settings, drive)
	} else {
		settings[drive] = setting
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.drivesPath(), append(data, '\n'))
}

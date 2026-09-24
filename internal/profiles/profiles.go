// Package profiles keeps the named settings a person makes and edits.
//
// Profiles written by hand in the settings file are read there and left alone:
// they are somebody's file, with their comments and their arrangement, and
// rewriting it to change a number would be presumptuous. Profiles made here
// live in a file of their own that ARFABIT owns outright.
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/config"
)

// Profile is a named set of settings, with where it came from.
type Profile struct {
	config.Profile

	// Editable says whether ARFABIT may change this one. Profiles from the
	// settings file are not: that file belongs to the person who wrote it.
	Editable bool `json:"editable"`

	// Source says where it came from, so the page can explain why a profile
	// cannot be edited here rather than merely refusing.
	Source string `json:"source"`

	Created time.Time `json:"created,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

// Store holds the profiles ARFABIT owns.
type Store struct {
	path string

	// Saved are the ones made here, by name.
	Saved map[string]Profile `json:"profiles"`
}

// Open reads the store, creating nothing until something is saved.
func Open(dataDir string) (*Store, error) {
	s := &Store{
		path:  filepath.Join(dataDir, "profiles.json"),
		Saved: map[string]Profile{},
	}

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}

	if err := json.Unmarshal(data, s); err != nil {
		// A damaged file starts empty rather than stopping ARFABIT: profiles
		// are a convenience, and the settings file still has its own.
		s.Saved = map[string]Profile{}
		return s, fmt.Errorf("the saved profiles could not be read: %w", err)
	}
	if s.Saved == nil {
		s.Saved = map[string]Profile{}
	}

	return s, nil
}

// Save writes or replaces a profile.
func (s *Store) Save(p config.Profile) (Profile, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Profile{}, fmt.Errorf("a profile needs a name")
	}
	if err := validate(p); err != nil {
		return Profile{}, err
	}

	p.Name = name
	saved := Profile{Profile: p, Editable: true, Source: "made here", Updated: time.Now()}

	if existing, ok := s.Saved[name]; ok {
		saved.Created = existing.Created
	} else {
		saved.Created = saved.Updated
	}

	s.Saved[name] = saved
	return saved, s.write()
}

// Delete removes a profile.
func (s *Store) Delete(name string) error {
	if _, ok := s.Saved[name]; !ok {
		return fmt.Errorf("there is no profile called %s", name)
	}
	delete(s.Saved, name)
	return s.write()
}

// All returns every profile ARFABIT knows about: the default, any written in
// the settings file, and any made here.
//
// Ordered with the default first and the rest by name, so the list does not
// rearrange itself between visits.
func (s *Store) All(cfg config.Config) []Profile {
	// Everything is editable. Changing one here keeps a version in ARFABIT's
	// own file, which takes precedence; the settings file is left as it was
	// written, for anybody who prefers to work that way.
	def := Profile{Profile: cfg.Profile, Editable: true, Source: "your settings file"}
	if saved, ok := s.Saved[cfg.Profile.Name]; ok {
		def = saved
	}
	out := []Profile{def}

	var fromFile []Profile
	for name, p := range cfg.Profiles {
		if name == cfg.Profile.Name {
			continue
		}
		if _, made := s.Saved[name]; made {
			// A profile made here with the same name is the one that wins,
			// and is listed below.
			continue
		}
		fromFile = append(fromFile, Profile{Profile: p, Editable: true, Source: "your settings file"})
	}
	sort.Slice(fromFile, func(a, b int) bool { return fromFile[a].Name < fromFile[b].Name })

	made := make([]Profile, 0, len(s.Saved))
	for _, p := range s.Saved {
		made = append(made, p)
	}
	sort.Slice(made, func(a, b int) bool { return made[a].Name < made[b].Name })

	return append(append(out, fromFile...), made...)
}

// Named finds a profile by name, preferring one made here.
func (s *Store) Named(cfg config.Config, name string) (config.Profile, bool) {
	if p, ok := s.Saved[name]; ok {
		return p.Profile, true
	}
	return cfg.ProfileNamed(name)
}

// validate refuses settings that would fail much later, when a film is already
// half converted.
func validate(p config.Profile) error {
	valid := map[string]bool{
		"superfast": true, "medium": true, "slow": true, "slower": true, "veryslow": true,
	}
	if !valid[p.Preset] {
		return fmt.Errorf("%q is not one of the speeds ARFABIT offers", p.Preset)
	}

	for _, crf := range []struct {
		what  string
		value int
	}{
		{"4K", p.CRFUHD},
		{"Blu-ray", p.CRFBluray},
		{"DVD", p.CRFDVD},
	} {
		if crf.value < 0 || crf.value > 51 {
			return fmt.Errorf("the %s quality of %d is outside the range 0 to 51", crf.what, crf.value)
		}
	}

	return nil
}

// write saves the store.
func (s *Store) write() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Write and rename, so a reader never sees half a file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

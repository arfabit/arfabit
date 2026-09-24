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

	// Default is the profile a disc gets unless something says otherwise.
	// Empty means the one named in the settings file.
	Default string `json:"default,omitempty"`

	// Hidden are profiles from the settings file that somebody has removed
	// here.
	//
	// ARFABIT does not rewrite the settings file — that file is somebody's,
	// with their comments and their arrangement — so removing one written
	// there means no longer offering it. The line stays in the file and does
	// nothing, which is what "removed" means from where the person is sitting.
	Hidden []string `json:"hidden,omitempty"`
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

// SetDefault chooses which profile a disc gets unless something says
// otherwise.
func (s *Store) SetDefault(cfg config.Config, name string) error {
	if !s.exists(cfg, name) {
		return fmt.Errorf("there is no profile called %s", name)
	}

	s.Default = name
	return s.write()
}

// DefaultName is the profile currently chosen as the default.
func (s *Store) DefaultName(cfg config.Config) string {
	// Checked directly rather than through All, which needs to know the
	// default in order to sort by it.
	if s.Default != "" && s.knows(cfg, s.Default) {
		return s.Default
	}
	return cfg.Profile.Name
}

// knows reports whether a profile exists and has not been removed here.
func (s *Store) knows(cfg config.Config, name string) bool {
	if s.hidden(name) {
		return false
	}
	if _, made := s.Saved[name]; made {
		return true
	}
	if name == cfg.Profile.Name {
		return true
	}
	_, fromFile := cfg.Profiles[name]
	return fromFile
}

// DefaultProfile is the settings a disc gets unless something says otherwise.
func (s *Store) DefaultProfile(cfg config.Config) config.Profile {
	p, _ := s.Named(cfg, s.DefaultName(cfg))
	return p
}

// Delete removes a profile.
//
// One written in the settings file is hidden rather than deleted, since
// ARFABIT does not rewrite that file. Either way it stops being offered,
// which is what removing it means to the person doing it.
func (s *Store) Delete(cfg config.Config, name string) error {
	if !s.exists(cfg, name) {
		return fmt.Errorf("there is no profile called %s", name)
	}

	if name == s.DefaultName(cfg) {
		return fmt.Errorf("%s is the one used by default; make another the default first", name)
	}
	if len(s.All(cfg)) <= 1 {
		return fmt.Errorf("%s is the only profile there is", name)
	}

	if _, made := s.Saved[name]; made {
		delete(s.Saved, name)
	} else {
		// From the settings file, so it is hidden instead.
		s.Hidden = append(s.Hidden, name)
	}

	return s.write()
}

// exists reports whether a profile is known and not hidden.
func (s *Store) exists(cfg config.Config, name string) bool {
	return s.knows(cfg, name)
}

// hidden reports whether a name has been removed here.
func (s *Store) hidden(name string) bool {
	for _, h := range s.Hidden {
		if h == name {
			return true
		}
	}
	return false
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
	byName := map[string]Profile{}

	if !s.hidden(cfg.Profile.Name) {
		byName[cfg.Profile.Name] = Profile{
			Profile:  cfg.Profile,
			Editable: true,
			Source:   "your settings file",
		}
	}
	for name, p := range cfg.Profiles {
		if s.hidden(name) {
			continue
		}
		byName[name] = Profile{Profile: p, Editable: true, Source: "your settings file"}
	}

	// A version kept here wins over one in the settings file.
	for name, p := range s.Saved {
		byName[name] = p
	}

	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}

	// The default first, the rest by name, so the list does not rearrange
	// itself between visits.
	def := s.DefaultName(cfg)
	sort.Slice(out, func(a, b int) bool {
		if (out[a].Name == def) != (out[b].Name == def) {
			return out[a].Name == def
		}
		return out[a].Name < out[b].Name
	})

	return out
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

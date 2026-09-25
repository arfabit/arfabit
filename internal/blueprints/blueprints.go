// Package blueprints keeps the starter templates for planning a job.
//
// A blueprint overrides some of the defaults. There may be none at all, in
// which case every Plan starts from the defaults; one may be chosen to be used
// by default, in which case every new Plan starts from it instead.
//
// Blueprints written by hand in the settings file are read there and left alone:
// they are somebody's file, with their comments and their arrangement, and
// rewriting it to change a number would be presumptuous. Blueprints made here
// live in a file of their own that ARFABIT owns outright.
package blueprints

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

// Blueprint is a named set of settings, with where it came from.
type Blueprint struct {
	config.Blueprint

	// Editable says whether this one can be changed on the page. Today every
	// blueprint can: changing one from the settings file keeps a version in
	// blueprints.json, and the file itself is left as it was written.
	Editable bool `json:"editable"`

	// Source says where it came from, so the page can explain why a blueprint
	// cannot be edited here rather than merely refusing.
	Source string `json:"source"`

	Created time.Time `json:"created,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

// Store holds the blueprints ARFABIT owns.
type Store struct {
	path string

	// Saved are the ones made here, by name.
	Saved map[string]Blueprint `json:"blueprints"`

	// Default is the blueprint a new Plan starts from. Empty means none: a
	// Plan then starts from the defaults alone.
	Default string `json:"default,omitempty"`

	// Hidden are blueprints from the settings file that somebody has removed
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
		path:  filepath.Join(dataDir, "blueprints.json"),
		Saved: map[string]Blueprint{},
	}

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}

	if err := json.Unmarshal(data, s); err != nil {
		// A damaged file starts empty rather than stopping ARFABIT: blueprints
		// are a convenience, and the settings file still has its own.
		s.Saved = map[string]Blueprint{}
		return s, fmt.Errorf("the saved blueprints could not be read: %w", err)
	}
	if s.Saved == nil {
		s.Saved = map[string]Blueprint{}
	}

	return s, nil
}

// Save writes or replaces a blueprint.
func (s *Store) Save(p config.Blueprint) (Blueprint, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Blueprint{}, fmt.Errorf("a blueprint needs a name")
	}
	if err := validate(p); err != nil {
		return Blueprint{}, err
	}

	p.Name = name
	saved := Blueprint{Blueprint: p, Editable: true, Source: "made here", Updated: time.Now()}

	if existing, ok := s.Saved[name]; ok {
		saved.Created = existing.Created
	} else {
		saved.Created = saved.Updated
	}

	s.Saved[name] = saved
	return saved, s.write()
}

// SetDefault chooses the blueprint a new Plan starts from. An empty name
// chooses none, so Plans start from the defaults alone.
func (s *Store) SetDefault(cfg config.Config, name string) error {
	if name != "" && !s.exists(cfg, name) {
		return fmt.Errorf("there is no blueprint called %s", name)
	}

	s.Default = name
	return s.write()
}

// DefaultName is the blueprint a new Plan starts from, or empty for none.
func (s *Store) DefaultName(cfg config.Config) string {
	// Checked directly rather than through All, which needs to know the
	// default in order to sort by it.
	if s.Default != "" && s.exists(cfg, s.Default) {
		return s.Default
	}
	return ""
}

// ForPlan is what a new Plan starts from: the default blueprint when one is
// chosen, and otherwise the defaults, which name no blueprint.
func (s *Store) ForPlan(cfg config.Config) config.Blueprint {
	if name := s.DefaultName(cfg); name != "" {
		if p, ok := s.Named(cfg, name); ok {
			return p
		}
	}
	return cfg.Plain()
}

// Delete removes a blueprint.
//
// One written in the settings file is hidden rather than deleted, since
// ARFABIT does not rewrite that file. Either way it stops being offered,
// which is what removing it means to the person doing it. Removing the one
// used by default leaves none used by default.
func (s *Store) Delete(cfg config.Config, name string) error {
	if !s.exists(cfg, name) {
		return fmt.Errorf("there is no blueprint called %s", name)
	}

	if _, made := s.Saved[name]; made {
		delete(s.Saved, name)
	}
	if _, fromFile := cfg.Blueprints[name]; fromFile {
		s.Hidden = append(s.Hidden, name)
	}
	if s.Default == name {
		s.Default = ""
	}

	return s.write()
}

// exists reports whether a blueprint is known and has not been removed here.
func (s *Store) exists(cfg config.Config, name string) bool {
	if _, made := s.Saved[name]; made {
		return true
	}
	_, fromFile := cfg.Blueprints[name]
	return fromFile && !s.hidden(name)
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

// All returns every blueprint ARFABIT knows about: any written in the settings
// file, and any made here. There may be none.
//
// Ordered with the default first and the rest by name, so the list does not
// rearrange itself between visits.
func (s *Store) All(cfg config.Config) []Blueprint {
	// Everything is editable. Changing one here keeps a version in ARFABIT's
	// own file, which takes precedence; the settings file is left as it was
	// written, for anybody who prefers to work that way.
	byName := map[string]Blueprint{}

	for name, p := range cfg.Blueprints {
		if s.hidden(name) {
			continue
		}
		byName[name] = Blueprint{Blueprint: p, Editable: true, Source: "your settings file"}
	}

	// A version kept here wins over one in the settings file.
	for name, p := range s.Saved {
		byName[name] = p
	}

	out := make([]Blueprint, 0, len(byName))
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

// Named finds a blueprint by name, preferring one made here.
func (s *Store) Named(cfg config.Config, name string) (config.Blueprint, bool) {
	if !s.exists(cfg, name) {
		return config.Blueprint{}, false
	}
	if p, ok := s.Saved[name]; ok {
		return p.Blueprint, true
	}
	return cfg.BlueprintNamed(name)
}

// validate refuses settings that would fail much later, when a film is already
// half converted.
func validate(p config.Blueprint) error {
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

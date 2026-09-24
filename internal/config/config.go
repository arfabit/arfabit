// Package config loads ARFABIT's layered settings.
//
// Settings come from, in increasing precedence: built-in defaults, the shared
// file in the data directory, and the local file for this machine. A Plan may
// then override anything for one disc, which happens above this package.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Config is the whole of ARFABIT's settings.
type Config struct {
	Node   Node
	Paths  Paths
	Server Server

	// Profile is what a disc gets unless something says otherwise.
	Profile Profile

	// Profiles are the named settings available to choose between, including
	// the default. Every one starts from the default and changes what it
	// names, so a profile that only differs in quality says only that.
	Profiles map[string]Profile

	// Sources records where each setting came from, so the UI can show
	// provenance and the user never has to guess which file to edit.
	Sources map[string]string
}

// Node identifies this machine.
type Node struct {
	ID   string
	Name string
}

// Paths are the directories ARFABIT reads and writes.
type Paths struct {
	// Data holds jobs, logs and the shared configuration. May be on a NAS.
	Data string

	// Masters holds the untouched rips. Never removed by ARFABIT.
	Masters string

	// Library holds the finished files, laid out the way Plex expects.
	Library string

	// Lab holds the test clips. A folder of its own, and a visible one:
	// the clips exist to be carried to a television and watched.
	Lab string

	// Deliver is an optional folder to copy finished files into. Empty by
	// default, which leaves them in Library.
	Deliver string
}

// Server is the web interface.
type Server struct {
	Addr string
}

// Profile is the one ruleset day one ships. More arrive as data, not code.
type Profile struct {
	Name string

	// CRF per source type. Lower means higher quality and larger files.
	CRFUHD    int
	CRFBluray int
	CRFDVD    int

	Preset string

	// AudioBitrate for the stereo fallback track.
	AudioBitrate string

	// CopyNativeAudio passes AC-3, E-AC-3 and AAC through untouched.
	CopyNativeAudio bool

	// AllowUHDCopy offers a direct copy for UHD discs, which are already HEVC.
	AllowUHDCopy bool

	// Subtitles selects which subtitle tracks to carry.
	IncludeForcedSubs bool
	IncludeFullSubs   bool
	SubLanguages      []string

	// MinTitleLength hides titles shorter than this during a scan.
	MinTitleLength time.Duration

	// ConvertAfterRip decides whether a disc becomes a film straight away, or
	// stops at the copy.
	//
	// Stopping at the copy is the fast way through a stack of discs: only the
	// copy needs the drive, and converting can be done later from the copy.
	ConvertAfterRip bool

	// MaxConversions is how many films may be converted at once, counting
	// lab clips. One is right for most machines: x265 already uses every
	// core, so a second conversion makes both later rather than either
	// sooner.
	MaxConversions int
}

// Defaults returns the built-in settings, before any file is read.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "Downloads", "arfabit")

	return Config{
		Node: Node{
			ID:   defaultNodeID(),
			Name: defaultNodeID(),
		},
		Paths: Paths{
			Data:    defaultDataDir(),
			Masters: filepath.Join(root, "masters"),
			Library: filepath.Join(root, "library"),
			Lab:     filepath.Join(root, "lab"),
		},
		Server: Server{
			// All interfaces, so the UI is reachable from any device in the
			// house, which is the point of having one.
			Addr: ":7847",
		},
		Profile: Profile{
			Name:              "Archive",
			CRFUHD:            20,
			CRFBluray:         20,
			CRFDVD:            18,
			Preset:            "slow",
			AudioBitrate:      "256k",
			CopyNativeAudio:   true,
			AllowUHDCopy:      true,
			IncludeForcedSubs: true,
			IncludeFullSubs:   true,
			SubLanguages:      []string{"eng"},
			ConvertAfterRip:   true,
			MinTitleLength:    120 * time.Second,
			MaxConversions:    1,
		},
		Profiles: map[string]Profile{},
		Sources:  map[string]string{},
	}
}

// ProfileNames lists the profiles in a settled order, the default first.
func (c Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		if name != c.Profile.Name {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	return append([]string{c.Profile.Name}, names...)
}

// ProfileNamed finds a profile by name, falling back to the default.
func (c Config) ProfileNamed(name string) (Profile, bool) {
	if name == "" || name == c.Profile.Name {
		return c.Profile, true
	}
	if p, ok := c.Profiles[name]; ok {
		return p, true
	}
	return c.Profile, false
}

// Describe summarises a profile in one line, for choosing between them.
func (p Profile) Describe() string {
	picture := fmt.Sprintf("HEVC quality %d, %s", p.CRFBluray, p.Preset)
	if p.AllowUHDCopy {
		picture += "; 4K kept as-is"
	}

	sound := "sound converted"
	if p.CopyNativeAudio {
		sound = "Dolby kept as-is"
	}

	return picture + " · " + sound
}

// Load reads the layered configuration.
//
// Missing files are not errors: a fresh install has none, and the defaults are
// meant to work without any.
func Load(localPath string) (Config, error) {
	cfg := Defaults()
	for key := range cfg.Sources {
		delete(cfg.Sources, key)
	}

	// The shared file lives in the data directory, which the local file may
	// itself relocate — so the local file is read first for that one setting.
	if localPath != "" {
		if doc, err := readFile(localPath); err != nil {
			return cfg, err
		} else if doc != nil {
			if v, ok := doc.lookup("paths", "data"); ok {
				cfg.Paths.Data = expand(v.asString())
			}
		}
	}

	shared := filepath.Join(cfg.Paths.Data, "config.toml")
	for _, layer := range []struct{ path, label string }{
		{shared, "shared settings"},
		{localPath, "this computer's settings"},
	} {
		if layer.path == "" {
			continue
		}
		doc, err := readFile(layer.path)
		if err != nil {
			return cfg, err
		}
		if doc == nil {
			continue
		}
		if err := cfg.apply(doc, layer.label); err != nil {
			return cfg, fmt.Errorf("%s: %w", layer.path, err)
		}
	}

	// The default profile is one of the profiles, so everything that chooses
	// between them has the whole list.
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.Profiles[cfg.Profile.Name] = cfg.Profile

	return cfg, cfg.Validate()
}

// readFile parses one settings file, returning nil when it does not exist.
func readFile(path string) (document, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	doc, err := parseTOML(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// apply folds one layer over the config, recording where each setting came from.
func (c *Config) apply(doc document, source string) error {
	str := func(section, key string, dst *string) {
		if v, ok := doc.lookup(section, key); ok {
			*dst = v.asString()
			c.note(section, key, source)
		}
	}
	path := func(section, key string, dst *string) {
		if v, ok := doc.lookup(section, key); ok {
			*dst = expand(v.asString())
			c.note(section, key, source)
		}
	}
	num := func(section, key string, dst *int) error {
		v, ok := doc.lookup(section, key)
		if !ok {
			return nil
		}
		n, err := v.asInt()
		if err != nil {
			return fmt.Errorf("line %d: %s.%s should be a whole number", v.line, section, key)
		}
		*dst = n
		c.note(section, key, source)
		return nil
	}
	boolean := func(section, key string, dst *bool) error {
		v, ok := doc.lookup(section, key)
		if !ok {
			return nil
		}
		b, err := v.asBool()
		if err != nil {
			return fmt.Errorf("line %d: %s.%s should be true or false", v.line, section, key)
		}
		*dst = b
		c.note(section, key, source)
		return nil
	}

	str("node", "name", &c.Node.Name)
	str("server", "addr", &c.Server.Addr)
	path("paths", "data", &c.Paths.Data)
	path("paths", "masters", &c.Paths.Masters)
	path("paths", "library", &c.Paths.Library)
	path("paths", "lab", &c.Paths.Lab)
	path("paths", "deliver", &c.Paths.Deliver)
	str("profile", "name", &c.Profile.Name)
	str("profile", "preset", &c.Profile.Preset)
	str("profile", "audio_bitrate", &c.Profile.AudioBitrate)

	for _, n := range []struct {
		key string
		dst *int
	}{
		{"crf_uhd", &c.Profile.CRFUHD},
		{"crf_bluray", &c.Profile.CRFBluray},
		{"crf_dvd", &c.Profile.CRFDVD},
		{"max_conversions", &c.Profile.MaxConversions},
	} {
		if err := num("profile", n.key, n.dst); err != nil {
			return err
		}
	}

	for _, b := range []struct {
		key string
		dst *bool
	}{
		{"copy_native_audio", &c.Profile.CopyNativeAudio},
		{"allow_uhd_copy", &c.Profile.AllowUHDCopy},
		{"include_forced_subs", &c.Profile.IncludeForcedSubs},
		{"include_full_subs", &c.Profile.IncludeFullSubs},
		{"convert_after_rip", &c.Profile.ConvertAfterRip},
	} {
		if err := boolean("profile", b.key, b.dst); err != nil {
			return err
		}
	}

	if v, ok := doc.lookup("profile", "sub_languages"); ok {
		c.Profile.SubLanguages = v.list
		c.note("profile", "sub_languages", source)
	}
	// Sections like [profile.Small] are profiles of their own. Each starts
	// from the default and changes only what it names.
	for section := range doc {
		name, ok := strings.CutPrefix(section, "profile.")
		if !ok || name == "" {
			continue
		}

		named := c.Profile
		named.Name = strings.Trim(name, `"`)
		if err := applyProfile(doc, section, &named); err != nil {
			return err
		}

		if c.Profiles == nil {
			c.Profiles = map[string]Profile{}
		}
		c.Profiles[named.Name] = named
		c.note("profiles", named.Name, source)
	}

	if v, ok := doc.lookup("profile", "min_title_seconds"); ok {
		n, err := v.asInt()
		if err != nil {
			return fmt.Errorf("line %d: profile.min_title_seconds should be a whole number", v.line)
		}
		c.Profile.MinTitleLength = time.Duration(n) * time.Second
		c.note("profile", "min_title_seconds", source)
	}

	return nil
}

func (c *Config) note(section, key, source string) {
	if c.Sources == nil {
		c.Sources = map[string]string{}
	}
	c.Sources[section+"."+key] = source
}

// SourceOf reports where a setting came from, for the UI.
func (c Config) SourceOf(section, key string) string {
	if s, ok := c.Sources[section+"."+key]; ok {
		return s
	}
	return "built-in default"
}

// applyProfile reads one profile's settings over a copy of the default.
func applyProfile(doc document, section string, p *Profile) error {
	if v, ok := doc.lookup(section, "preset"); ok {
		p.Preset = v.asString()
	}
	if v, ok := doc.lookup(section, "audio_bitrate"); ok {
		p.AudioBitrate = v.asString()
	}

	for _, n := range []struct {
		key string
		dst *int
	}{
		{"crf_uhd", &p.CRFUHD},
		{"crf_bluray", &p.CRFBluray},
		{"crf_dvd", &p.CRFDVD},
	} {
		v, ok := doc.lookup(section, n.key)
		if !ok {
			continue
		}
		number, err := v.asInt()
		if err != nil {
			return fmt.Errorf("line %d: %s.%s should be a whole number", v.line, section, n.key)
		}
		*n.dst = number
	}

	for _, b := range []struct {
		key string
		dst *bool
	}{
		{"copy_native_audio", &p.CopyNativeAudio},
		{"allow_uhd_copy", &p.AllowUHDCopy},
		{"include_forced_subs", &p.IncludeForcedSubs},
		{"include_full_subs", &p.IncludeFullSubs},
	} {
		v, ok := doc.lookup(section, b.key)
		if !ok {
			continue
		}
		value, err := v.asBool()
		if err != nil {
			return fmt.Errorf("line %d: %s.%s should be true or false", v.line, section, b.key)
		}
		*b.dst = value
	}

	if v, ok := doc.lookup(section, "sub_languages"); ok {
		p.SubLanguages = v.list
	}

	return nil
}

// Validate checks settings that would otherwise fail much later, when a disc
// is already spinning.
func (c Config) Validate() error {
	validPresets := map[string]bool{
		"superfast": true, "medium": true, "slow": true, "slower": true, "veryslow": true,
	}
	if !validPresets[c.Profile.Preset] {
		return fmt.Errorf("profile.preset is %q; it should be one of superfast, medium, slow, slower, veryslow", c.Profile.Preset)
	}

	for _, crf := range []struct {
		name  string
		value int
	}{
		{"crf_uhd", c.Profile.CRFUHD},
		{"crf_bluray", c.Profile.CRFBluray},
		{"crf_dvd", c.Profile.CRFDVD},
	} {
		if crf.value < 0 || crf.value > 51 {
			return fmt.Errorf("profile.%s is %d; it should be between 0 and 51", crf.name, crf.value)
		}
	}

	if c.Paths.Masters == "" || c.Paths.Library == "" {
		return fmt.Errorf("paths.masters and paths.library are both needed")
	}
	return nil
}

// CRFFor returns the quality setting for a source type.
func (p Profile) CRFFor(kind string) int {
	switch kind {
	case "uhd":
		return p.CRFUHD
	case "dvd":
		return p.CRFDVD
	default:
		return p.CRFBluray
	}
}

// expand resolves ~ and environment-independent relative paths.
func expand(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// DefaultLocalPath is where this machine's own settings file lives.
func DefaultLocalPath() string {
	return filepath.Join(defaultDataDir(), "config.local.toml")
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "arfabit-data"
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "arfabit")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "arfabit")
		}
		return filepath.Join(home, "arfabit")
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "arfabit")
		}
		return filepath.Join(home, ".config", "arfabit")
	}
}

func defaultNodeID() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "this-computer"
	}
	// Hostnames become directory names under nodes/, so keep them simple.
	name = strings.TrimSuffix(name, ".local")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, name)
}

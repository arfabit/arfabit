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
	"strings"
	"time"
)

// Config is the whole of ARFABIT's settings.
type Config struct {
	Node    Node
	Paths   Paths
	Server  Server
	Machine Machine
	Drive   Drive
	MakeMKV MakeMKV

	// Defaults are the settings a Plan starts from, for ripping and
	// transcoding alike, when no blueprint is used.
	Defaults Settings

	// Blueprints are the ones written in the settings file. Each starts from
	// the defaults and changes only what it names, so a blueprint that only
	// differs in quality says only that. There may be none.
	Blueprints map[string]Blueprint

	// Sources records where each setting came from, so the UI can show
	// provenance and the user never has to guess which file to edit.
	Sources map[string]string

	// written holds each blueprint's lines until every file is read.
	written map[string]map[string]value
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

	// Library holds the originals and everything made from them, one folder
	// per film, laid out the way Plex expects.
	Library string

	// Deliver is an optional folder to copy finished files into. Empty by
	// default, which leaves them in Library.
	Deliver string
}

// Server is the web interface.
type Server struct {
	Addr string
}

// Machine is what belongs to this computer's processor.
type Machine struct {
	// MaxConversions is how many films may be converted at once, counting
	// lab clips. One is right for most machines: x265 already uses every
	// core, so a second conversion makes both later rather than either
	// sooner.
	MaxConversions int
}

// Drive is what belongs to the disc drives on this computer.
type Drive struct {
	// ReadCacheMB is how much MakeMKV buffers while reading a disc. Zero
	// leaves the choice to MakeMKV, which is almost always right.
	ReadCacheMB int
}

// MakeMKV is how discs are read, the same on every computer.
type MakeMKV struct {
	// MinTitleLength hides titles shorter than this during a scan.
	MinTitleLength time.Duration
}

// Blueprint is a starter template for planning a job: named settings that
// override the defaults. Applying one copies its values into a Plan; a job
// never refers back to the blueprint it came from.
type Blueprint struct {
	Name string

	// Edition is what a Plan filled in from this blueprint puts in its
	// edition. It starts as the blueprint's name and may be changed or left
	// blank.
	Edition string

	Settings
}

// Settings are what fills in a Plan.
// The choices for TrueHD.
const (
	TrueHDKeep = "keep"
	TrueHDFLAC = "flac"
	TrueHDBoth = "both"
)

// SoundRules chooses sound tracks by language, layout and quality.
//
// Read as a sentence: for the languages (one of them, or all of them), make
// each choice in turn. A choice either picks one track — the widest of the
// layouts it names that is on the disc — or keeps every track that fits.
type SoundRules struct {
	// Languages are three-letter codes in order of preference. Empty means
	// every language on the disc.
	Languages []string `json:"languages"`

	// LanguageMode is SoundOne for the first of Languages the disc has, or
	// SoundAll for each of them.
	LanguageMode string `json:"language_mode"`

	// Choices are made for each language, in order.
	Choices []SoundChoice `json:"choices"`
}

// SoundChoice is one thing to look for in each language.
type SoundChoice struct {
	// Mode is SoundOne to pick one track, or SoundAll to keep every track
	// that fits.
	Mode string `json:"mode"`

	// Layouts are "7.1", "5.1" and "stereo", widest first. Empty means any.
	Layouts []string `json:"layouts"`

	// Quality is "lossless", "lossy", or empty for either.
	Quality string `json:"quality"`
}

// The two ways a rule can choose.
const (
	SoundOne = "one"
	SoundAll = "all"
)

type Settings struct {
	// CRF per source type. Lower means higher quality and larger files.
	CRFUHD    int
	CRFBluray int
	CRFDVD    int

	Preset string

	// AudioBitrate for the stereo fallback track.
	AudioBitrate string

	// CopyNativeAudio passes sound that plays directly through untouched
	// (§4). Off, lossy sound is converted as well.
	CopyNativeAudio bool

	// AllowUHDCopy offers a direct copy for UHD discs, which are already HEVC.
	AllowUHDCopy bool

	// KeepPicture keeps every picture exactly as it is, whatever the disc.
	// It plays directly (§4) and takes minutes rather than hours, but a
	// Blu-ray's picture stays the size it is on the disc.
	KeepPicture bool

	// TrueHD is what to do with Dolby TrueHD sound, which Plex converts every
	// time it plays on an Apple TV (§4): TrueHDKeep, TrueHDFLAC, or TrueHDBoth
	// for one of each. Keeping it is the default; changing it is the user's
	// choice to make.
	TrueHD string

	// Subtitles selects which subtitle tracks to carry.
	IncludeForcedSubs bool
	IncludeFullSubs   bool
	SubLanguages      []string

	// KeepSubtitlePictures copies a Blu-ray's picture subtitles as they are.
	// Off, they are read into text (SRT) wherever the computer can (§10),
	// since showing pictures makes Plex convert the whole picture (§4).
	KeepSubtitlePictures bool

	// Sound, when set, chooses which sound tracks a transcode keeps, by what
	// they are rather than where they sit on one particular disc. When it is
	// not set, the stereo-first choice described in §9 is made instead.
	//
	// Sound rules are made on the page and kept in blueprints.json. The
	// settings file cannot hold them: its reader is a small stand-in that is
	// not to be extended (§16).
	Sound *SoundRules

	// ConvertAfterRip decides whether a disc becomes a film straight away, or
	// stops at the copy.
	//
	// Stopping at the copy is the fast way through a stack of discs: only the
	// copy needs the drive, and converting can be done later from the copy.
	ConvertAfterRip bool
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
			Library: root,
		},
		Server: Server{
			// All interfaces, so the UI is reachable from any device in the
			// house, which is the point of having one.
			Addr: ":7847",
		},
		Machine: Machine{MaxConversions: 1},
		MakeMKV: MakeMKV{MinTitleLength: 120 * time.Second},
		Defaults: Settings{
			CRFUHD:            20,
			CRFBluray:         20,
			CRFDVD:            18,
			Preset:            "slow",
			AudioBitrate:      "256k",
			CopyNativeAudio:   true,
			AllowUHDCopy:      true,
			TrueHD:            TrueHDKeep,
			IncludeForcedSubs: true,
			IncludeFullSubs:   true,
			SubLanguages:      []string{"eng"},
			ConvertAfterRip:   true,
		},
		Blueprints: map[string]Blueprint{},
		Sources:    map[string]string{},
	}
}

// BlueprintNamed finds a blueprint written in the settings file.
func (c Config) BlueprintNamed(name string) (Blueprint, bool) {
	p, ok := c.Blueprints[name]
	return p, ok
}

// Plain is the defaults as a Blueprint with no name, for anything that takes
// a Blueprint. A Plan filled in from it names no blueprint.
func (c Config) Plain() Blueprint {
	return Blueprint{Settings: c.Defaults}
}

// Describe summarises settings in one line, for choosing between them.
func (p Settings) Describe() string {
	picture := fmt.Sprintf("HEVC quality %d, %s", p.CRFBluray, p.Preset)
	if p.AllowUHDCopy {
		picture += "; 4K kept as-is"
	}

	sound := "audio converted"
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

	for name, keys := range cfg.written {
		p := cfg.Plain()
		p.Name = name
		p.Edition = name
		if v, ok := keys["edition"]; ok {
			p.Edition = v.asString()
		}
		section := "blueprint." + name
		if err := applySettings(document{section: keys}, section, &p.Settings, func(string) {}); err != nil {
			return cfg, err
		}
		cfg.Blueprints[name] = p
	}
	cfg.written = nil

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
	str("node", "name", &c.Node.Name)
	str("server", "addr", &c.Server.Addr)
	path("paths", "data", &c.Paths.Data)
	path("paths", "library", &c.Paths.Library)
	path("paths", "deliver", &c.Paths.Deliver)
	if err := num("machine", "max_conversions", &c.Machine.MaxConversions); err != nil {
		return err
	}
	if err := num("drive", "read_cache_mb", &c.Drive.ReadCacheMB); err != nil {
		return err
	}
	seconds := -1
	if err := num("makemkv", "min_title_seconds", &seconds); err != nil {
		return err
	}
	if seconds >= 0 {
		c.MakeMKV.MinTitleLength = time.Duration(seconds) * time.Second
	}

	if err := applySettings(doc, "defaults", &c.Defaults, func(key string) {
		c.note("defaults", key, source)
	}); err != nil {
		return err
	}

	// Sections like [blueprint.Small] are blueprints. Each starts from the
	// defaults, as they stand once every file is read, and changes only what
	// it names — so they are read last.
	for section := range doc {
		name, ok := strings.CutPrefix(section, "blueprint.")
		if !ok || name == "" {
			continue
		}
		name = strings.Trim(name, `"`)
		if c.written == nil {
			c.written = map[string]map[string]value{}
		}
		if c.written[name] == nil {
			c.written[name] = map[string]value{}
		}
		// A later file changes the keys it gives and keeps the rest.
		for key, v := range doc[section] {
			c.written[name][key] = v
		}
		c.note("blueprints", name, source)
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

// applySettings reads the settings in one section over what is already there.
// noted is told each key that was given.
func applySettings(doc document, section string, p *Settings, noted func(key string)) error {
	for _, t := range []struct {
		key string
		dst *string
	}{
		{"preset", &p.Preset},
		{"audio_bitrate", &p.AudioBitrate},
		{"truehd", &p.TrueHD},
	} {
		if v, ok := doc.lookup(section, t.key); ok {
			*t.dst = v.asString()
			noted(t.key)
		}
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
		noted(n.key)
	}

	for _, b := range []struct {
		key string
		dst *bool
	}{
		{"copy_native_audio", &p.CopyNativeAudio},
		{"allow_uhd_copy", &p.AllowUHDCopy},
		{"keep_picture", &p.KeepPicture},
		{"include_forced_subs", &p.IncludeForcedSubs},
		{"include_full_subs", &p.IncludeFullSubs},
		{"keep_subtitle_pictures", &p.KeepSubtitlePictures},
		{"convert_after_rip", &p.ConvertAfterRip},
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
		noted(b.key)
	}

	if v, ok := doc.lookup(section, "sub_languages"); ok {
		p.SubLanguages = v.list
		noted("sub_languages")
	}

	return nil
}

// Validate checks settings that would otherwise fail much later, when a disc
// is already spinning.
func (c Config) Validate() error {
	validPresets := map[string]bool{
		"superfast": true, "medium": true, "slow": true, "slower": true, "veryslow": true,
	}
	if !validPresets[c.Defaults.Preset] {
		return fmt.Errorf("defaults.preset is %q; it should be one of superfast, medium, slow, slower, veryslow", c.Defaults.Preset)
	}
	switch c.Defaults.TrueHD {
	case TrueHDKeep, TrueHDFLAC, TrueHDBoth:
	default:
		return fmt.Errorf("defaults.truehd is %q; it should be keep, flac or both", c.Defaults.TrueHD)
	}

	for _, crf := range []struct {
		name  string
		value int
	}{
		{"crf_uhd", c.Defaults.CRFUHD},
		{"crf_bluray", c.Defaults.CRFBluray},
		{"crf_dvd", c.Defaults.CRFDVD},
	} {
		if crf.value < 0 || crf.value > 51 {
			return fmt.Errorf("defaults.%s is %d; it should be between 0 and 51", crf.name, crf.value)
		}
	}

	if c.Paths.Library == "" {
		return fmt.Errorf("paths.library is needed")
	}
	return nil
}

// CRFFor returns the quality setting for a source type.
func (p Settings) CRFFor(kind string) int {
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

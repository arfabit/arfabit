package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTOML(t *testing.T) {
	doc, err := parseTOML(strings.NewReader(`
# a comment
[profile]
name = "Archive"
crf_bluray = 20
copy_native_audio = true
sub_languages = ["eng", "fra"]
note = "a # inside a string is not a comment"
`))
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := doc.lookup("profile", "name"); v.asString() != "Archive" {
		t.Errorf("name = %q", v.asString())
	}
	if v, _ := doc.lookup("profile", "crf_bluray"); func() int { n, _ := v.asInt(); return n }() != 20 {
		t.Error("crf_bluray did not parse as 20")
	}
	if v, _ := doc.lookup("profile", "copy_native_audio"); func() bool { b, _ := v.asBool(); return b }() != true {
		t.Error("copy_native_audio did not parse as true")
	}
	if v, _ := doc.lookup("profile", "sub_languages"); len(v.list) != 2 || v.list[0] != "eng" {
		t.Errorf("sub_languages = %v", v.list)
	}
	if v, _ := doc.lookup("profile", "note"); !strings.Contains(v.asString(), "#") {
		t.Errorf("a # inside a string was treated as a comment: %q", v.asString())
	}
}

// The reader handles a deliberate subset, and must say so rather than silently
// misreading something it does not support.
func TestParseTOMLRejectsUnsupportedSyntax(t *testing.T) {
	for _, in := range []string{
		"[[profiles]]\nname = \"x\"",
		"[profile]\nlimits = { crf = 20 }",
		"[profile\nname = \"x\"",
		"[profile]\njust some words",
	} {
		if _, err := parseTOML(strings.NewReader(in)); err == nil {
			t.Errorf("accepted unsupported syntax:\n%s", in)
		}
	}
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Errorf("built-in defaults do not validate: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nothing-here.toml"))
	if err != nil {
		t.Fatalf("a missing settings file should not be an error: %v", err)
	}
	if cfg.Profile.CRFBluray != 20 {
		t.Errorf("CRFBluray = %d, want the default 20", cfg.Profile.CRFBluray)
	}
}

func TestLoadLocalOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "config.local.toml")
	write(t, local, `
[node]
name = "the-mac-mini"

[profile]
crf_bluray = 18
preset = "slower"
sub_languages = ["eng", "jpn"]
min_title_seconds = 300
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Node.Name != "the-mac-mini" {
		t.Errorf("Name = %q", cfg.Node.Name)
	}
	if cfg.Profile.CRFBluray != 18 {
		t.Errorf("CRFBluray = %d, want 18", cfg.Profile.CRFBluray)
	}
	if cfg.Profile.Preset != "slower" {
		t.Errorf("Preset = %q", cfg.Profile.Preset)
	}
	if len(cfg.Profile.SubLanguages) != 2 {
		t.Errorf("SubLanguages = %v", cfg.Profile.SubLanguages)
	}
	if cfg.Profile.MinTitleLength != 5*time.Minute {
		t.Errorf("MinTitleLength = %v", cfg.Profile.MinTitleLength)
	}
	// Untouched settings keep their defaults.
	if cfg.Profile.CRFDVD != 18 {
		t.Errorf("CRFDVD = %d, want the default", cfg.Profile.CRFDVD)
	}
}

// The UI shows where each setting came from so nobody has to guess which file
// to edit.
func TestSourceProvenance(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "config.local.toml")
	write(t, local, "[profile]\ncrf_bluray = 16\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.SourceOf("profile", "crf_bluray"); got != "this computer's settings" {
		t.Errorf("SourceOf(crf_bluray) = %q", got)
	}
	if got := cfg.SourceOf("profile", "crf_dvd"); got != "built-in default" {
		t.Errorf("SourceOf(crf_dvd) = %q", got)
	}
}

// The shared file may live on a NAS, and the local file is what says where.
func TestSharedLayerBeneathLocal(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "shared")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(data, "config.toml"), "[profile]\npreset = \"veryslow\"\ncrf_bluray = 22\n")

	local := filepath.Join(dir, "config.local.toml")
	write(t, local, "[paths]\ndata = \""+data+"\"\n\n[profile]\ncrf_bluray = 19\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	// Shared provides the preset; local wins on the setting both define.
	if cfg.Profile.Preset != "veryslow" {
		t.Errorf("Preset = %q, want veryslow from the shared file", cfg.Profile.Preset)
	}
	if cfg.Profile.CRFBluray != 19 {
		t.Errorf("CRFBluray = %d, want 19 from the local file", cfg.Profile.CRFBluray)
	}
	if got := cfg.SourceOf("profile", "preset"); got != "shared settings" {
		t.Errorf("SourceOf(preset) = %q", got)
	}
}

// Bad settings must be caught at startup, not when a disc is already spinning.
func TestValidateCatchesBadSettings(t *testing.T) {
	cfg := Defaults()
	cfg.Profile.Preset = "ultrafast"
	if err := cfg.Validate(); err == nil {
		t.Error("an unsupported preset was accepted")
	}

	cfg = Defaults()
	cfg.Profile.CRFBluray = 99
	if err := cfg.Validate(); err == nil {
		t.Error("an out-of-range CRF was accepted")
	}
}

func TestCRFFor(t *testing.T) {
	p := Defaults().Profile
	if p.CRFFor("uhd") != 20 || p.CRFFor("bluray") != 20 || p.CRFFor("dvd") != 18 {
		t.Errorf("CRFFor returned unexpected values: uhd=%d bluray=%d dvd=%d",
			p.CRFFor("uhd"), p.CRFFor("bluray"), p.CRFFor("dvd"))
	}
}

func TestNodeIDIsPathSafe(t *testing.T) {
	// Node IDs become directory names under nodes/.
	id := defaultNodeID()
	if id == "" {
		t.Fatal("empty node id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			t.Errorf("node id %q contains %q, which is not safe in a path", id, r)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
[blueprint.Archive]
preset = "slow"
crf_bluray = 20
copy_native_audio = true
sub_languages = ["eng", "fra"]
note = "a # inside a string is not a comment"
`))
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := doc.lookup("blueprint.Archive", "preset"); v.asString() != "slow" {
		t.Errorf("preset = %q", v.asString())
	}
	if v, _ := doc.lookup("blueprint.Archive", "crf_bluray"); func() int { n, _ := v.asInt(); return n }() != 20 {
		t.Error("crf_bluray did not parse as 20")
	}
	if v, _ := doc.lookup("blueprint.Archive", "copy_native_audio"); func() bool { b, _ := v.asBool(); return b }() != true {
		t.Error("copy_native_audio did not parse as true")
	}
	if v, _ := doc.lookup("blueprint.Archive", "sub_languages"); len(v.list) != 2 || v.list[0] != "eng" {
		t.Errorf("sub_languages = %v", v.list)
	}
	if v, _ := doc.lookup("blueprint.Archive", "note"); !strings.Contains(v.asString(), "#") {
		t.Errorf("a # inside a string was treated as a comment: %q", v.asString())
	}
}

// The reader handles a deliberate subset, and must say so rather than silently
// misreading something it does not support.
func TestParseTOMLRejectsUnsupportedSyntax(t *testing.T) {
	for _, in := range []string{
		"[[blueprints]]\nname = \"x\"",
		"[defaults]\nlimits = { crf = 20 }",
		"[blueprint\nname = \"x\"",
		"[defaults]\njust some words",
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
	if cfg.Defaults.CRFBluray != 20 {
		t.Errorf("CRFBluray = %d, want the default 20", cfg.Defaults.CRFBluray)
	}
}

func TestLoadLocalOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "config.local.toml")
	write(t, local, `
[node]
name = "the-mac-mini"

[defaults]
crf_bluray = 18
preset = "slower"
sub_languages = ["eng", "jpn"]

[makemkv]
min_title_seconds = 300
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Node.Name != "the-mac-mini" {
		t.Errorf("Name = %q", cfg.Node.Name)
	}
	if cfg.Defaults.CRFBluray != 18 {
		t.Errorf("CRFBluray = %d, want 18", cfg.Defaults.CRFBluray)
	}
	if cfg.Defaults.Preset != "slower" {
		t.Errorf("Preset = %q", cfg.Defaults.Preset)
	}
	if len(cfg.Defaults.SubLanguages) != 2 {
		t.Errorf("SubLanguages = %v", cfg.Defaults.SubLanguages)
	}
	if cfg.MakeMKV.MinTitleLength != 5*time.Minute {
		t.Errorf("MinTitleLength = %v", cfg.MakeMKV.MinTitleLength)
	}
	// Untouched settings keep their defaults.
	if cfg.Defaults.CRFDVD != 18 {
		t.Errorf("CRFDVD = %d, want the default", cfg.Defaults.CRFDVD)
	}
}

// The UI shows where each setting came from so nobody has to guess which file
// to edit.
func TestSourceProvenance(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "config.local.toml")
	write(t, local, "[defaults]\ncrf_bluray = 16\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.SourceOf("defaults", "crf_bluray"); got != "this computer's settings" {
		t.Errorf("SourceOf(crf_bluray) = %q", got)
	}
	if got := cfg.SourceOf("defaults", "crf_dvd"); got != "built-in default" {
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
	write(t, filepath.Join(data, "config.toml"), "[defaults]\npreset = \"veryslow\"\ncrf_bluray = 22\n")

	local := filepath.Join(dir, "config.local.toml")
	write(t, local, "[paths]\ndata = \""+data+"\"\n\n[defaults]\ncrf_bluray = 19\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	// Shared provides the preset; local wins on the setting both define.
	if cfg.Defaults.Preset != "veryslow" {
		t.Errorf("Preset = %q, want veryslow from the shared file", cfg.Defaults.Preset)
	}
	if cfg.Defaults.CRFBluray != 19 {
		t.Errorf("CRFBluray = %d, want 19 from the local file", cfg.Defaults.CRFBluray)
	}
	if got := cfg.SourceOf("defaults", "preset"); got != "shared settings" {
		t.Errorf("SourceOf(preset) = %q", got)
	}
}

// Bad settings must be caught at startup, not when a disc is already spinning.
func TestValidateCatchesBadSettings(t *testing.T) {
	cfg := Defaults()
	cfg.Defaults.Preset = "ultrafast"
	if err := cfg.Validate(); err == nil {
		t.Error("an unsupported preset was accepted")
	}

	cfg = Defaults()
	cfg.Defaults.CRFBluray = 99
	if err := cfg.Validate(); err == nil {
		t.Error("an out-of-range CRF was accepted")
	}
}

func TestCRFFor(t *testing.T) {
	p := Defaults().Defaults
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

// A section like [blueprint.Small] is a blueprint: a name, and the settings it
// changes from the defaults. There need not be any.
func TestNamedBlueprints(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "config.local.toml")
	write(t, local, `
[defaults]
crf_bluray = 20
preset = "slow"

[blueprint.Small]
crf_bluray = 24
preset = "medium"

[blueprint.Exact]
allow_uhd_copy = true
copy_native_audio = true
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Blueprints) != 2 {
		t.Fatalf("got %d blueprints, want the two named: %v", len(cfg.Blueprints), cfg.Blueprints)
	}

	// A blueprint changes what it names and keeps the rest.
	small, ok := cfg.BlueprintNamed("Small")
	if !ok {
		t.Fatal("Small was not found")
	}
	if small.CRFBluray != 24 || small.Preset != "medium" {
		t.Errorf("Small = crf %d, preset %q", small.CRFBluray, small.Preset)
	}
	if small.CRFDVD != cfg.Defaults.CRFDVD {
		t.Errorf("Small changed a setting it did not name: crf_dvd = %d", small.CRFDVD)
	}
	if small.Name != "Small" {
		t.Errorf("Small is named %q", small.Name)
	}
}

// With no blueprints at all, everything still works from the defaults.
func TestNoBlueprints(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Blueprints) != 0 {
		t.Errorf("blueprints appeared from nowhere: %v", cfg.Blueprints)
	}
	if plain := cfg.Plain(); plain.Name != "" || plain.CRFBluray != cfg.Defaults.CRFBluray {
		t.Errorf("the plain defaults are %+v", plain)
	}
	if _, ok := cfg.BlueprintNamed("Anything"); ok {
		t.Error("a blueprint that does not exist was found")
	}
}

// A blueprint builds on the defaults as they stand once every file is read,
// so a machine's own defaults carry into a blueprint shared from elsewhere.
func TestBlueprintBuildsOnTheFinalDefaults(t *testing.T) {
	data := t.TempDir()
	write(t, filepath.Join(data, "config.toml"), "[blueprint.Small]\ncrf_bluray = 24\n")

	local := filepath.Join(t.TempDir(), "config.local.toml")
	write(t, local, "[paths]\ndata = \""+data+"\"\n\n[defaults]\ncrf_dvd = 15\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	small := cfg.Blueprints["Small"]
	if small.CRFBluray != 24 || small.CRFDVD != 15 {
		t.Errorf("Small = bluray %d, dvd %d; want 24 and the local default 15", small.CRFBluray, small.CRFDVD)
	}
}

// Choosing between blueprints means reading a line about each.
func TestBlueprintDescribe(t *testing.T) {
	p := Defaults().Defaults
	got := p.Describe()

	for _, want := range []string{"quality 20", "slow", "audio as it is", "subtitles as text", "MKV"} {
		if !strings.Contains(got, want) {
			t.Errorf("the description %q is missing %q", got, want)
		}
	}
}

func TestMachineDriveAndMakeMKVSettings(t *testing.T) {
	local := filepath.Join(t.TempDir(), "config.local.toml")
	write(t, local, `
[machine]
max_conversions = 2

[drive]
read_cache_mb = 256

[makemkv]
min_title_seconds = 60
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Machine.MaxConversions != 2 || cfg.Drive.ReadCacheMB != 256 || cfg.MakeMKV.MinTitleLength != time.Minute {
		t.Errorf("got %d conversions, %d MB cache, %v minimum", cfg.Machine.MaxConversions, cfg.Drive.ReadCacheMB, cfg.MakeMKV.MinTitleLength)
	}
}

// A blueprint's edition starts as its name, and can be changed or cleared.
func TestBlueprintEdition(t *testing.T) {
	local := filepath.Join(t.TempDir(), "config.local.toml")
	write(t, local, `
[blueprint.Small]
crf_bluray = 24

[blueprint.Cut]
edition = "Director's Cut"

[blueprint.Plain]
edition = ""
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"Small": "Small", "Cut": "Director's Cut", "Plain": ""} {
		if got := cfg.Blueprints[name].Edition; got != want {
			t.Errorf("%s has edition %q, want %q", name, got, want)
		}
	}
	if cfg.Plain().Edition != "" {
		t.Errorf("the defaults have edition %q, want none", cfg.Plain().Edition)
	}
}

// The annotated example is a real settings file: it loads, and every value
// in it is one ARFABIT accepts.
func TestExampleSettingsLoad(t *testing.T) {
	cfg, err := Load("../../config.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := cfg.Defaults; d.Video != VideoConvert || d.Audio != AudioCopy || d.Subtitles != SubtitlesText || d.Container != "mkv" {
		t.Errorf("defaults = %+v", d)
	}
}

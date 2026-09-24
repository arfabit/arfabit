package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
)

func testProfile(name string) config.Profile {
	p := config.Defaults().Profile
	p.Name = name
	return p
}

func TestSaveAndReopen(t *testing.T) {
	dir := t.TempDir()

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	p := testProfile("Small")
	p.CRFBluray = 24
	p.Preset = "medium"

	if _, err := store.Save(p); err != nil {
		t.Fatal(err)
	}

	// The file is the record, so a fresh store finds it.
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	saved, ok := reopened.Saved["Small"]
	if !ok {
		t.Fatal("Small did not survive being written and read back")
	}
	if saved.CRFBluray != 24 || saved.Preset != "medium" {
		t.Errorf("round trip lost settings: %+v", saved.Profile)
	}
	if !saved.Editable {
		t.Error("a profile made here is not editable")
	}
	if saved.Created.IsZero() {
		t.Error("a profile made here has no creation time")
	}
}

// Editing keeps the original creation time: it is the same profile.
func TestEditingKeepsWhenItWasMade(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.Save(testProfile("Small"))
	if err != nil {
		t.Fatal(err)
	}

	changed := testProfile("Small")
	changed.CRFBluray = 26

	second, err := store.Save(changed)
	if err != nil {
		t.Fatal(err)
	}

	if !second.Created.Equal(first.Created) {
		t.Error("editing a profile changed when it was made")
	}
	if !second.Updated.After(first.Updated) && second.Updated.Equal(first.Updated) {
		t.Error("editing a profile did not change when it was last touched")
	}
}

func TestDelete(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testProfile("Temporary")); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("Temporary"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Saved["Temporary"]; ok {
		t.Error("the profile is still there after being deleted")
	}

	if err := store.Delete("Temporary"); err == nil {
		t.Error("deleting something that is not there reported success")
	}
}

// Settings that would fail halfway through converting a film are refused now.
func TestSaveRefusesNonsense(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	unnamed := testProfile("")
	if _, err := store.Save(unnamed); err == nil {
		t.Error("a profile with no name was saved")
	}

	fast := testProfile("Fast")
	fast.Preset = "ultrafast"
	if _, err := store.Save(fast); err == nil {
		t.Error("an unsupported speed was saved")
	}

	silly := testProfile("Silly")
	silly.CRFBluray = 99
	if _, err := store.Save(silly); err == nil {
		t.Error("an impossible quality was saved")
	} else if !strings.Contains(err.Error(), "0 to 51") {
		t.Errorf("the message does not say what the range is: %v", err)
	}
}

// Every profile can be changed here, including one written by hand and
// including the default. Changing one keeps a version in ARFABIT's own file,
// which takes precedence; the settings file is left as it was written.
func TestEveryProfileCanBeChanged(t *testing.T) {
	cfg := config.Defaults()
	cfg.Profiles = map[string]config.Profile{
		cfg.Profile.Name: cfg.Profile,
		"FromFile":       testProfile("FromFile"),
	}

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testProfile("MadeHere")); err != nil {
		t.Fatal(err)
	}

	all := store.All(cfg)
	byName := map[string]Profile{}
	for _, p := range all {
		byName[p.Name] = p
	}

	for _, name := range []string{cfg.Profile.Name, "FromFile", "MadeHere"} {
		if !byName[name].Editable {
			t.Errorf("%s cannot be changed here", name)
		}
	}
	if !strings.Contains(byName["FromFile"].Source, "settings file") {
		t.Errorf("Source = %q; it should still say where it came from", byName["FromFile"].Source)
	}

	// A version kept here wins over the one in the settings file.
	changed := testProfile(cfg.Profile.Name)
	changed.CRFBluray = 26
	if _, err := store.Save(changed); err != nil {
		t.Fatal(err)
	}

	for _, p := range store.All(cfg) {
		if p.Name == cfg.Profile.Name && p.CRFBluray != 26 {
			t.Errorf("the default still reads %d; the version kept here should win", p.CRFBluray)
		}
	}

	// The default comes first, so the list does not rearrange between visits.
	if all[0].Name != cfg.Profile.Name {
		t.Errorf("the list starts with %q, want the default", all[0].Name)
	}
}

// A damaged file starts empty rather than stopping ARFABIT.
func TestDamagedFileDoesNotStopAnything(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err == nil {
		t.Error("a damaged file was read without complaint")
	}
	if store == nil || store.Saved == nil {
		t.Fatal("a damaged file left nothing usable")
	}
	if _, err := store.Save(testProfile("Works")); err != nil {
		t.Errorf("a damaged file stopped new profiles being made: %v", err)
	}
}

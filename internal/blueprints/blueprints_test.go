package blueprints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
)

func testBlueprint(name string) config.Blueprint {
	p := config.Defaults().Plain()
	p.Name = name
	return p
}

func TestSaveAndReopen(t *testing.T) {
	dir := t.TempDir()

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	p := testBlueprint("Small")
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
		t.Errorf("round trip lost settings: %+v", saved.Blueprint)
	}
	if !saved.Editable {
		t.Error("a blueprint made here is not editable")
	}
	if saved.Created.IsZero() {
		t.Error("a blueprint made here has no creation time")
	}
}

// Editing keeps the original creation time: it is the same blueprint.
func TestEditingKeepsWhenItWasMade(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.Save(testBlueprint("Small"))
	if err != nil {
		t.Fatal(err)
	}

	changed := testBlueprint("Small")
	changed.CRFBluray = 26

	second, err := store.Save(changed)
	if err != nil {
		t.Fatal(err)
	}

	if !second.Created.Equal(first.Created) {
		t.Error("editing a blueprint changed when it was made")
	}
	if !second.Updated.After(first.Updated) && second.Updated.Equal(first.Updated) {
		t.Error("editing a blueprint did not change when it was last touched")
	}
}

func TestDelete(t *testing.T) {
	cfg := config.Defaults()

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testBlueprint("Temporary")); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(cfg, "Temporary"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Saved["Temporary"]; ok {
		t.Error("the blueprint is still there after being deleted")
	}

	if err := store.Delete(cfg, "Temporary"); err == nil {
		t.Error("deleting something that is not there reported success")
	}
}

// With no blueprints, and none chosen, a Plan starts from the defaults and
// names no blueprint.
func TestNoBlueprintIsNeeded(t *testing.T) {
	cfg := config.Defaults()

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(store.All(cfg)) != 0 {
		t.Errorf("blueprints appeared from nowhere: %v", store.All(cfg))
	}
	if got := store.DefaultName(cfg); got != "" {
		t.Errorf("DefaultName = %q with nothing chosen", got)
	}

	start := store.ForPlan(cfg)
	if start.Name != "" || start.CRFBluray != cfg.Defaults.CRFBluray {
		t.Errorf("a Plan would start from %+v, want the defaults with no name", start)
	}
}

// One of several blueprints can be used by default, and new Plans then start
// from it. Removing it, or choosing none, returns them to the defaults.
func TestADefaultBlueprintIsOptional(t *testing.T) {
	cfg := config.Defaults()
	cfg.Blueprints = map[string]config.Blueprint{"FromFile": testBlueprint("FromFile")}

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mine := testBlueprint("Mine")
	mine.CRFBluray = 22
	for _, p := range []config.Blueprint{mine, testBlueprint("Other")} {
		if _, err := store.Save(p); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetDefault(cfg, "Mine"); err != nil {
		t.Fatal(err)
	}
	if got := store.ForPlan(cfg); got.Name != "Mine" || got.CRFBluray != 22 {
		t.Errorf("a Plan would start from %q at %d, want Mine at 22", got.Name, got.CRFBluray)
	}
	if all := store.All(cfg); all[0].Name != "Mine" {
		t.Errorf("the list starts with %q, want the one used by default", all[0].Name)
	}

	// Choosing none goes back to the defaults.
	if err := store.SetDefault(cfg, ""); err != nil {
		t.Fatal(err)
	}
	if got := store.ForPlan(cfg); got.Name != "" {
		t.Errorf("a Plan would start from %q after choosing none", got.Name)
	}

	// The one used by default can be removed, which leaves none used by
	// default.
	if err := store.SetDefault(cfg, "FromFile"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(cfg, "FromFile"); err != nil {
		t.Fatalf("the default could not be removed: %v", err)
	}
	if got := store.DefaultName(cfg); got != "" {
		t.Errorf("DefaultName = %q after removing it", got)
	}

	// Removing one from the settings file survives a restart: the line left
	// in the file does not bring it back.
	reopened, err := Open(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reopened.All(cfg) {
		if p.Name == "FromFile" {
			t.Error("the removed blueprint came back after a restart")
		}
	}
	if _, ok := reopened.Named(cfg, "FromFile"); ok {
		t.Error("the removed blueprint can still be found by name")
	}
}

// Choosing something that is not there is refused rather than silently
// leaving no default at all.
func TestDefaultMustExist(t *testing.T) {
	cfg := config.Defaults()

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetDefault(cfg, "Nonexistent"); err == nil {
		t.Error("a blueprint that does not exist was made the default")
	}
	if got := store.DefaultName(cfg); got != "" {
		t.Errorf("DefaultName = %q; it should have been left alone", got)
	}
}

// Settings that would fail halfway through converting a film are refused now.
func TestSaveRefusesNonsense(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	unnamed := testBlueprint("")
	if _, err := store.Save(unnamed); err == nil {
		t.Error("a blueprint with no name was saved")
	}

	fast := testBlueprint("Fast")
	fast.Preset = "ultrafast"
	if _, err := store.Save(fast); err == nil {
		t.Error("an unsupported speed was saved")
	}

	silly := testBlueprint("Silly")
	silly.CRFBluray = 99
	if _, err := store.Save(silly); err == nil {
		t.Error("an impossible quality was saved")
	} else if !strings.Contains(err.Error(), "0 to 51") {
		t.Errorf("the message does not say what the range is: %v", err)
	}
}

// Every blueprint can be changed here, including one written by hand.
// Changing one keeps a version in ARFABIT's own file, which takes precedence;
// the settings file is left as it was written.
func TestEveryBlueprintCanBeChanged(t *testing.T) {
	cfg := config.Defaults()
	cfg.Blueprints = map[string]config.Blueprint{"FromFile": testBlueprint("FromFile")}

	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(testBlueprint("MadeHere")); err != nil {
		t.Fatal(err)
	}

	byName := map[string]Blueprint{}
	for _, p := range store.All(cfg) {
		byName[p.Name] = p
	}

	for _, name := range []string{"FromFile", "MadeHere"} {
		if !byName[name].Editable {
			t.Errorf("%s cannot be changed here", name)
		}
	}
	if !strings.Contains(byName["FromFile"].Source, "settings file") {
		t.Errorf("Source = %q; it should still say where it came from", byName["FromFile"].Source)
	}

	// A version kept here wins over the one in the settings file.
	changed := testBlueprint("FromFile")
	changed.CRFBluray = 26
	if _, err := store.Save(changed); err != nil {
		t.Fatal(err)
	}

	for _, p := range store.All(cfg) {
		if p.Name == "FromFile" && p.CRFBluray != 26 {
			t.Errorf("FromFile still reads %d; the version kept here should win", p.CRFBluray)
		}
	}
}

// A damaged file starts empty rather than stopping ARFABIT.
func TestDamagedFileDoesNotStopAnything(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blueprints.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err == nil {
		t.Error("a damaged file was read without complaint")
	}
	if store == nil || store.Saved == nil {
		t.Fatal("a damaged file left nothing usable")
	}
	if _, err := store.Save(testBlueprint("Works")); err != nil {
		t.Errorf("a damaged file stopped new blueprints being made: %v", err)
	}
}

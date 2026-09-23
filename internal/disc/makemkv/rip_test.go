package makemkv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgressTracker(t *testing.T) {
	var tr progressTracker

	// Names arrive before values and set the context for them.
	for _, line := range []string{
		`PRGT:5018,0,"Saving 1 titles into directory /out"`,
		`PRGC:5017,0,"Analyzing seamless segments"`,
	} {
		rec, _ := ParseLine(line)
		if _, ok := tr.apply(rec); ok {
			t.Errorf("%q produced a progress update; only PRGV should", line)
		}
	}

	rec, _ := ParseLine(`PRGV:16384,32768,65536`)
	p, ok := tr.apply(rec)
	if !ok {
		t.Fatal("PRGV produced no update")
	}

	if p.Operation != "Analyzing seamless segments" {
		t.Errorf("Operation = %q", p.Operation)
	}
	if p.Title != "Saving 1 titles into directory /out" {
		t.Errorf("Title = %q", p.Title)
	}
	if got := p.CurrentPercent(); got != 25 {
		t.Errorf("CurrentPercent = %v, want 25", got)
	}
	if got := p.TotalPercent(); got != 50 {
		t.Errorf("TotalPercent = %v, want 50", got)
	}
}

// Max of zero must not divide by zero; it means "no information yet".
func TestProgressZeroMax(t *testing.T) {
	p := Progress{Current: 10, Total: 10, Max: 0}
	if p.CurrentPercent() != 0 || p.TotalPercent() != 0 {
		t.Error("zero Max should yield zero percentages, not a division by zero")
	}
}

func TestRipArgs(t *testing.T) {
	b := &Backend{}
	args := b.ripArgs(RipRequest{DriveIndex: 0, Titles: []int{3}, OutputDir: "/out"})
	joined := strings.Join(args, " ")

	for _, want := range []string{"-r", "--progress=-same", "mkv", "disc:0", "3", "/out"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}

// With no title named, makemkvcon is asked for everything.
func TestRipArgsAllTitles(t *testing.T) {
	b := &Backend{}
	args := b.ripArgs(RipRequest{DriveIndex: 1, OutputDir: "/out"})
	if got := args[len(args)-2]; got != "all" {
		t.Errorf("title selector = %q, want \"all\"", got)
	}
}

func TestTitleSelector(t *testing.T) {
	if got := titleSelector([]int{7}); got != "7" {
		t.Errorf("one title = %q, want \"7\"", got)
	}
	if got := titleSelector(nil); got != "all" {
		t.Errorf("no titles = %q, want \"all\"", got)
	}
	// Several titles cannot be expressed in one invocation, so Rip loops
	// instead; the selector is only ever called with one at a time.
	if got := titleSelector([]int{1, 2}); got != "all" {
		t.Errorf("multiple titles = %q, want \"all\"", got)
	}
}

// Output files are identified by diffing the directory, so a file that was
// already there is not reported as something this rip produced.
func TestNewFilesIgnoresPreexisting(t *testing.T) {
	dir := t.TempDir()
	before := map[string]bool{"old.mkv": true}
	after := map[string]bool{"old.mkv": true, "new.mkv": true, "another.mkv": true}

	got := newFiles(dir, before, after)
	want := []string{filepath.Join(dir, "another.mkv"), filepath.Join(dir, "new.mkv")}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("file %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMkvFilesIgnoresOtherExtensions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.mkv", "B.MKV", "notes.txt", "part.mkv.tmp"} {
		if err := writeEmpty(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	found, err := mkvFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Errorf("found %v, want only the two .mkv files", found)
	}
}

func writeEmpty(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}

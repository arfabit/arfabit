package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

func runnerWithFolders(t *testing.T) *Runner {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Paths.Library = filepath.Join(root, "library")
	cfg.Paths.Clips = filepath.Join(root, "clips")
	return &Runner{Config: cfg, Store: testStore(t), Calibration: NewCalibration()}
}

func place(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("somebody's film"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Plan that would replace an original or a film already there does not start,
// and says which file is in the way. Changing the edition is enough to get
// past a film, since the edition is part of its name.
func TestPlanDoesNotStartOverAFile(t *testing.T) {
	r := runnerWithFolders(t)
	title := meta.Title{Name: "In the Grey", Year: 2026}

	job := &Job{Job: store.NewJob("waiting")}
	job.Title, job.Year = title.Name, title.Year
	job.Plan = &store.Plan{RipName: "In the Grey_t00.mkv", Convert: true}
	job.Space = Space{Fits: true}
	job.State = store.StateWaiting
	r.SetCurrentForTest(job)

	if got := r.Existing(job); len(got) != 0 {
		t.Fatalf("found %v in empty folders", got)
	}

	film := filepath.Join(title.LibraryDir(r.Config.Paths.Library), title.VideoName(""))
	place(t, film)
	if got := r.Existing(job); len(got) != 1 || got[0] != film {
		t.Fatalf("Existing = %v, want [%s]", got, film)
	}
	if err := r.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "does not replace") {
		t.Fatalf("started over a film already in the library: %v", err)
	}

	job.Plan.Edition = "Archive"
	if got := r.Existing(job); len(got) != 0 {
		t.Errorf("a different edition still collides: %v", got)
	}

	// Stopping after the copy does not make a film, so only the original
	// counts: under its own name, or under MakeMKV's on the way to it.
	job.Plan.Edition, job.Plan.Convert = "", false
	folder := title.LibraryDir(r.Config.Paths.Library)
	for _, name := range []string{title.OriginalName(), "In the Grey_t00.mkv"} {
		original := filepath.Join(folder, name)
		place(t, original)
		if got := r.Existing(job); len(got) != 1 || got[0] != original {
			t.Errorf("Existing = %v, want only %s", got, name)
		}
		if err := os.Remove(original); err != nil {
			t.Fatal(err)
		}
	}

	if b, _ := os.ReadFile(film); string(b) != "somebody's film" {
		t.Error("the film already there was changed")
	}
}

// A project that would make a film already in the library is refused before
// it joins the line, whether that film was made as MKV or, before, as MP4.
func TestPackageDoesNotReplaceAFilm(t *testing.T) {
	r := runnerWithFolders(t)
	title := meta.Title{Name: "Crime 101", Year: 2025}
	req := ProjectRequest{
		Original: "/nowhere/original.mkv", Film: "Crime 101", Year: 2025,
		LibraryDir: r.Config.Paths.Library,
		Project:    store.Project{Items: []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}}},
	}

	for _, ext := range []string{".mkv", ".mp4"} {
		earlier := filepath.Join(title.LibraryDir(r.Config.Paths.Library), "Crime 101 (2025)"+ext)
		place(t, earlier)
		if _, err := r.StartProject(context.Background(), req); err == nil || !strings.Contains(err.Error(), "does not replace") {
			t.Fatalf("a film already there as %s was accepted: %v", ext, err)
		}
		if err := os.Remove(earlier); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.Active()) != 0 {
		t.Error("a refused package joined the queue")
	}
}

// A film is never given the edition the original is kept under, since the
// two would then have the same name.
func TestFilmIsNotCalledOriginal(t *testing.T) {
	p := store.Project{Edition: "original", Items: []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}}}
	if err := checkProject(&p, false); err == nil {
		t.Fatal("a film was allowed the original's edition")
	}
	p.Length = 30e9
	if err := checkProject(&p, false); err != nil {
		t.Errorf("a clip, which is named otherwise, was refused: %v", err)
	}
}

// The file MakeMKV made is renamed to the original's own name, beside the
// films; if something already has that name, nothing is replaced and the
// copy keeps MakeMKV's.
func TestOriginalIsRenamedIntoPlace(t *testing.T) {
	r := runnerWithFolders(t)
	title := meta.Title{Name: "In the Grey", Year: 2026}
	folder := title.LibraryDir(r.Config.Paths.Library)
	job := &Job{Job: store.NewJob("copy")}
	job.Log, _ = NewLog(filepath.Join(t.TempDir(), "log.txt"), nil)

	copied := filepath.Join(folder, "In the Grey_t00.mkv")
	place(t, copied)
	want := filepath.Join(folder, "In the Grey (2026) {edition-Original}.mkv")
	if got := r.nameOriginal(job, copied, want); got != want {
		t.Fatalf("the original is at %s, want %s", got, want)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Error("MakeMKV's name is still there after the rename")
	}

	again := filepath.Join(folder, "In the Grey_t01.mkv")
	place(t, again)
	if got := r.nameOriginal(job, again, want); got != again {
		t.Errorf("a second copy took the name of the first: %s", got)
	}
	if _, err := os.Stat(again); err != nil {
		t.Error("the second copy is gone")
	}
}

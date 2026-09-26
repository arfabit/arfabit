package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// copying makes a disc that has been started, being copied, with a film
// waiting for it.
func copying(t *testing.T, r *Runner) (*Job, *Job) {
	t.Helper()
	disc := &Job{Job: store.NewJob("copy")}
	disc.Title, disc.Year = "In the Grey", 2026
	disc.Stage = store.StageRip
	disc.Plan = &store.Plan{
		Convert: true, Read: []int{7},
		Tracks: []Track{
			{Index: 0, Kind: store.KindVideo},
			{Index: 7, Kind: store.KindSubtitle, Lang: "eng", Codec: pictureSubtitles},
			{Index: 9, Kind: store.KindSubtitle, Lang: "eng", Codec: pictureSubtitles},
		},
		Project: &store.Project{Items: []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}}},
	}
	disc.Log, _ = NewLog(r.Store.LogPath(disc.ID), nil)

	film := &Job{Job: store.NewJob("film")}
	film.Kind, film.Stage = store.KindConvert, store.StageQueued
	film.Title, film.Year = disc.Title, disc.Year
	film.Project = &store.Project{Items: []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}}}
	film.discTracks = disc.Plan.Tracks
	film.Log, _ = NewLog(r.Store.LogPath(film.ID), nil)

	disc.film = film
	r.planned = disc
	return disc, film
}

// While a disc is copied, its name and the subtitles to read can change, and
// so can its film while the film waits; each is refused once its step begins.
func TestPlanChangesUntilEachStepStarts(t *testing.T) {
	r := runnerWithFolders(t)
	disc, film := copying(t, r)

	if r.Editable() != disc {
		t.Fatal("a disc being copied cannot be changed")
	}
	if e := r.EditingOf(disc); !e.Started || !e.Name || !e.Read || !e.Film {
		t.Errorf("while copying: %+v", e)
	}

	if _, err := r.SetTitle("In the Gray", 2026); err != nil {
		t.Fatal(err)
	}
	if film.Title != "In the Gray" || !strings.HasPrefix(film.File, "In the Gray (2026)") {
		t.Errorf("the film is still called %s, %s", film.Title, film.File)
	}
	if err := r.SetRead(map[int]bool{7: false, 9: true}); err != nil || len(disc.Plan.Read) != 1 || disc.Plan.Read[0] != 9 {
		t.Errorf("read = %v, %v", disc.Plan.Read, err)
	}

	// The copy is finished: its name and subtitles are settled, and the
	// film can still change while it waits.
	disc.copied = true
	if _, err := r.SetTitle("Something Else", 0); err == nil {
		t.Error("the name changed after the copy was finished")
	}
	if err := r.SetRead(map[int]bool{7: true}); err == nil {
		t.Error("the subtitles to read changed after they had begun")
	}
	edited := store.Project{Edition: "Archive", Items: []store.Item{
		{Kind: store.KindVideo, Action: store.ActionConvert, To: "hevc", CRF: 22, Preset: "medium"},
	}}
	if err := r.UpdateProject(edited); err != nil {
		t.Fatal(err)
	}
	if film.Project.Edition != "Archive" || film.Project.Items[0].CRF != 22 || !strings.Contains(film.File, "{edition-Archive}") {
		t.Errorf("the waiting film is %+v, %s", film.Project, film.File)
	}

	// Once the film has left the line, it is what it was given.
	film.Stage = store.StagePackage
	if err := r.UpdateProject(store.Project{Items: edited.Items}); err == nil {
		t.Error("a film that had started was changed")
	}
	if r.Editable() != nil {
		t.Error("a Plan with nothing left to change is still offered")
	}
}

// A film changed after its original exists is pointed at the original's
// tracks, as it was when first planned.
func TestAChangedFilmIsPointedAtTheOriginal(t *testing.T) {
	r := runnerWithFolders(t)
	disc, film := copying(t, r)
	disc.copied = true
	film.bound = []Track{
		{Index: 0, Kind: store.KindVideo},
		{Index: 3, Kind: store.KindSubtitle, Lang: "eng", Codec: pictureSubtitles},
		{Index: 4, Kind: store.KindSubtitle, Lang: "eng", Codec: pictureSubtitles},
	}
	r.OCR = &readsInOrder{}

	if err := r.UpdateProject(store.Project{Items: []store.Item{
		{Kind: store.KindVideo, Action: store.ActionCopy},
		{Kind: store.KindSubtitle, Source: 9, Action: store.ActionConvert, To: "srt", Codec: pictureSubtitles, Lang: "eng"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := film.Project.Items[1].Source; got != 4 {
		t.Errorf("the second English track of the disc is the original's %d, want 4", got)
	}
	if got := disc.Plan.Project.Items[1].Source; got != 9 {
		t.Errorf("the Plan's own line item was changed to %d; it keeps the disc's numbers", got)
	}
}

// Turning the film off while a disc is copied takes it off the queue.
func TestFilmCanBeTurnedOffWhileCopying(t *testing.T) {
	r := runnerWithFolders(t)
	disc, film := copying(t, r)
	_, cancel := context.WithCancel(context.Background())
	film.cancel = cancel
	film.State = store.StateRunning

	if err := r.SetConvert(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if disc.Plan.Convert || disc.film != nil || film.State != store.StateStopped {
		t.Errorf("convert = %v, film %v, %s", disc.Plan.Convert, disc.film, film.State)
	}
}

// A name changed while copying puts the original in the folder for that name,
// and the folder made for the old one goes if it is empty.
func TestARenamedCopyMovesToItsFolder(t *testing.T) {
	r := runnerWithFolders(t)
	disc, _ := copying(t, r)
	old := meta.Title{Name: disc.Title, Year: disc.Year}.LibraryDir(r.Config.Paths.Library)
	copied := filepath.Join(old, "In the Grey_t00.mkv")
	place(t, copied)

	disc.Title = "In the Gray"
	got := r.placeOriginal(disc, copied, old, true)
	want := filepath.Join(r.Config.Paths.Library, "In the Gray (2026)", "In the Gray (2026) {edition-Original}.mkv")
	if got != want {
		t.Errorf("the original is at %s, want %s", got, want)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the empty folder made for the old name is still there")
	}

	// A folder that was there before, or has anything in it, stays.
	place(t, filepath.Join(old, "somebody's.txt"))
	again := filepath.Join(old, "In the Grey_t01.mkv")
	place(t, again)
	disc.copied, disc.Title = false, "Another Name"
	r.placeOriginal(disc, again, old, true)
	if _, err := os.Stat(filepath.Join(old, "somebody's.txt")); err != nil {
		t.Error("a folder with something in it was removed")
	}
}

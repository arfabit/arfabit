package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arfabit/arfabit/internal/meta"
)

// ARFABIT never replaces a file that is already there (§0.6). ffmpeg would,
// without a word, and so, possibly, would MakeMKV; so every file a job is about
// to make is looked for first, and a job that would replace one does not start.
// It is looked for again just before writing, because a job can wait in the
// line for hours and a file can appear in the meantime.

// errWouldReplace is returned when writing would replace a file.
var errWouldReplace = errors.New("a file with that name is already there")

// ReplaceError names the file that is already there.
type ReplaceError struct{ Path string }

func (e *ReplaceError) Error() string {
	return fmt.Sprintf("%s is already in %s", filepath.Base(e.Path), filepath.Dir(e.Path))
}

func (e *ReplaceError) Unwrap() error { return errWouldReplace }

// refuseToReplace reports a ReplaceError if something is already at path.
func refuseToReplace(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return &ReplaceError{Path: path}
	}
	return nil
}

// Existing lists the files a disc's Plan would replace if it were started now:
// the original, the file MakeMKV makes on the way to it, and the finished film
// if the Plan makes one.
func (r *Runner) Existing(job *Job) []string {
	if job == nil || job.Plan == nil {
		return nil
	}
	title := meta.Title{Name: job.Title, Year: job.Year}
	folder := title.LibraryDir(r.Config.Paths.Library)

	found := existingAt(filepath.Join(folder, title.OriginalName()))
	if job.Plan.RipName != "" {
		found = append(found, existingAt(filepath.Join(folder, job.Plan.RipName))...)
	}
	if job.Plan.Convert {
		found = append(found, existingFilm(title.LibraryDir(r.Config.Paths.Library), title, job.Plan.Edition)...)
	}
	return found
}

// existingFilm lists a film already in dir under this edition, as MKV or as
// MP4: to Plex both are this edition of this film.
func existingFilm(dir string, title meta.Title, edition string) []string {
	name := title.VideoName(edition)
	found := existingAt(filepath.Join(dir, name))
	stem := strings.TrimSuffix(name, meta.VideoExt)
	for _, ext := range meta.OtherVideoExts {
		found = append(found, existingAt(filepath.Join(dir, stem+ext))...)
	}
	return found
}

// existingAt lists path if something is there.
func existingAt(path string) []string {
	if refuseToReplace(path) != nil {
		return []string{path}
	}
	return nil
}

// wouldReplace says in a sentence which files are already there, for a job
// that will not start because of them.
func wouldReplace(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return fmt.Errorf("%s is already there, and ARFABIT does not replace files. Give this one a different edition, or move that file somewhere else, and start again",
		paths[0])
}

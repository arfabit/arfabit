package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/subs"
)

// pictureSubtitles is the one kind of subtitle ARFABIT reads into text: the
// pictures a Blu-ray carries (PGS). subs.ParseSUP reads nothing else.
const pictureSubtitles = "hdmv_pgs_subtitle"

// sidecarPaths are the SRT files a Package's converted subtitles become, by
// the Master stream they come from: beside the file made, named after it and
// the track's language, which is how Plex matches a sidecar to a film (§6).
// There is one per language; checkPackage refuses a second.
func sidecarPaths(pkg *store.Package, out string) map[int]string {
	stem := strings.TrimSuffix(out, filepath.Ext(out))
	paths := map[int]string{}
	for _, it := range pkg.ItemsOf(store.KindSubtitle) {
		if it.Action == store.ActionConvert {
			paths[it.Source] = stem + "." + ocr.Tag(it.Lang) + ".srt"
		}
	}
	return paths
}

// readSubtitles reads a Package's converted subtitle tracks into SRT files
// beside out, as stage OCR, once the file is made. It reads them from input,
// the file the Package was made from: a clip's piece starts where the clip
// does, so its subtitles need only trimming to its length. (A copied piece
// begins at the keyframe before the time asked for, not at it, so shifting
// the Master's times by that time would put every line early.)
//
// The file is made whatever happens here. What goes wrong with one track is
// said in the log, and in what it returns for the job's note; the rest are
// still read. Only a stop is returned as an error.
func (r *Runner) readSubtitles(ctx context.Context, job *Job, input string, index func(int) int, out string) (string, error) {
	pkg := job.Package
	sidecars := sidecarPaths(pkg, out)
	var items []store.Item
	for _, it := range pkg.ItemsOf(store.KindSubtitle) {
		if it.Action == store.ActionConvert {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return "", nil
	}

	was := job.Stage
	job.Stage = store.StageOCR
	defer func() { job.Stage = was }()

	var notes []string
	low := 0
	for n, it := range items {
		name := languageName(it.Lang)
		job.Progress = Progress{
			Since:     time.Now(),
			Percent:   float64(n) / float64(len(items)) * 100,
			Operation: fmt.Sprintf("Reading the %s subtitles", name),
		}
		job.Log.Printf(store.StageOCR, "Reading the %s subtitles into text.", name)
		r.save(job)

		found, err := r.readTrack(ctx, job, it, input, index, sidecars[it.Source], func(done, total int) {
			job.Progress.Percent = (float64(n) + float64(done)/float64(total)) / float64(len(items)) * 100
			r.notify(job)
		})
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var lang *ocr.LanguageError
		switch {
		case errors.As(err, &lang):
			job.Log.Printf(store.StageOCR, "%s", lang.Message)
			notes = append(notes, lang.Message)
		case err != nil:
			text := fmt.Sprintf("ARFABIT could not read the %s subtitles into text.", name)
			job.Log.Detail(store.StageOCR, text, detailOf(err))
			notes = append(notes, text)
		}
		low += found
	}

	switch {
	case low == 1:
		notes = append(notes, "One subtitle has low confidence; the log says which.")
	case low > 1:
		notes = append(notes, fmt.Sprintf("%d subtitles have low confidence; the log lists them.", low))
	}
	return strings.Join(notes, " "), nil
}

// readTrack reads one subtitle track into its sidecar, and lists and keeps
// the pictures of its subtitles of low confidence. It returns how many there
// are.
func (r *Runner) readTrack(ctx context.Context, job *Job, it store.Item, input string, index func(int) int, sidecar string, progress func(done, total int)) (int, error) {
	if r.OCR == nil {
		return 0, errors.New(ocr.NotAvailable)
	}
	name := languageName(it.Lang)

	subtitles, err := subs.ReadTrack(ctx, input, index(it.Source))
	if err != nil {
		return 0, err
	}
	result, err := ocr.ReadSubtitles(ctx, r.OCR, subtitles, it.Lang, "", progress)
	if err != nil {
		return 0, err
	}

	cues := result.Cues
	if !job.Package.WholeFilm() {
		cues = ocr.Trim(cues, job.Package.Length)
	}
	if len(cues) == 0 {
		job.Log.Printf(store.StageOCR, "No text was read from the %s subtitles, so there is no subtitle file for them.", name)
		return 0, nil
	}
	if err := writeSidecar(job, sidecar, []byte(subs.WriteSRT(cues))); err != nil {
		return 0, err
	}
	job.Sidecars = append(job.Sidecars, sidecar)
	job.Log.Printf(store.StageOCR, "Read %d %s subtitles into %s.", len(cues), name, filepath.Base(sidecar))

	found := 0
	for _, l := range result.LowConfidence {
		if !job.Package.WholeFilm() && l.Start >= job.Package.Length {
			continue
		}
		found++
		entry := store.LowConfidence{Sidecar: sidecar, Start: l.Start, End: l.End, Text: l.Text, Fast: l.Fast}

		var picture bytes.Buffer
		if err := png.Encode(&picture, subtitles[l.Index].Image); err == nil {
			file := fmt.Sprintf("%d-%05d.png", it.Source, l.Index)
			if path, err := r.Store.SavePicture(job.ID, file, picture.Bytes()); err == nil {
				entry.Picture = path
			}
		}
		job.LowConfidence = append(job.LowConfidence, entry)

		if l.Text == "" {
			job.Log.Printf(store.StageOCR, "Low confidence in the %s subtitles at %s: nothing could be read, so it is left out.", name, clock(l.Start))
		} else {
			job.Log.Printf(store.StageOCR, "Low confidence in the %s subtitles at %s: %q", name, clock(l.Start), l.Text)
		}
	}
	return found, nil
}

// writeSidecar writes an SRT beside the file it belongs to: whole, under a
// name of ARFABIT's own, then renamed into place, and never over a file that
// is already there.
func writeSidecar(job *Job, path string, data []byte) error {
	if err := refuseToReplace(path); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".arfabit-"+job.ID+"-"+filepath.Base(path))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// clock is a moment in a film as a player shows it: 1:02:03.
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

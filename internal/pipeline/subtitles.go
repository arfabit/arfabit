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

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/subs"
)

// pictureSubtitles is the one kind of subtitle ARFABIT reads into text: the
// pictures a Blu-ray carries (PGS). subs.ParseSUP reads nothing else.
const pictureSubtitles = "hdmv_pgs_subtitle"

// subtitleFilesOf makes a Project's subtitle files beside stem
// (subtitleFiles). A track copied as it is is taken out of input whole, as
// SUP or SRT. A track read into text from the whole of a source is a copy of
// the SRT beside the source (takeSubtitle). One from a stretch is read from
// the piece cut for it, on the stretch's own clock: shifting the source's
// times by the time asked for would put every line early by up to the gap
// between keyframes, since the piece starts at the keyframe before it.
//
// Subtitles that cannot be had never cost the rest: what went wrong is said
// in what it returns, for the note. Only a stop, or a file already there, is
// returned as an error.
func (r *Runner) subtitleFilesOf(ctx context.Context, job *Job, info *ffmpeg.MediaInfo, input string, index func(int) int, stem string) (string, error) {
	pkg := job.Project
	files := subtitleFiles(pkg, stem)
	tracks := OriginalTracks(info)

	var notes []string
	low := 0
	for i, it := range pkg.Items {
		path, ok := files[i]
		if !ok {
			continue
		}
		name := languageName(it.Lang)

		switch {
		case it.Action == store.ActionCopy:
			if err := takeOut(ctx, job, input, index(it.Source), path); err != nil {
				return "", err
			}
			job.Made = append(job.Made, path)
			job.Log.Printf(job.Stage, "Took the %s subtitles out, as %s.", name, filepath.Base(path))

		case pkg.Whole():
			if note := r.takeSubtitle(ctx, job, tracks, it, path); note != "" {
				notes = append(notes, note)
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

		default:
			was := job.Stage
			job.Stage = store.StageOCR
			job.Progress = Progress{Since: time.Now(), Operation: fmt.Sprintf("Reading the %s subtitles", name)}
			r.save(job)
			read, found, err := r.readInto(ctx, job, input, index(it.Source), it.Lang, pkg.Length, path, it.Source, func(done, total int) {
				job.Progress.Percent = float64(done) / float64(total) * 100
				r.notify(job)
			})
			job.Stage = was
			var lang *ocr.LanguageError
			var replace *ReplaceError
			switch {
			case ctx.Err() != nil:
				return "", ctx.Err()
			case errors.As(err, &replace):
				return "", err
			case errors.As(err, &lang):
				job.Log.Printf(store.StageOCR, "%s", lang.Message)
				notes = append(notes, lang.Message)
			case err != nil:
				text := fmt.Sprintf("ARFABIT could not read the %s subtitles into text.", name)
				job.Log.Detail(store.StageOCR, text, detailOf(err))
				notes = append(notes, text)
			case read == 0:
				job.Log.Printf(store.StageOCR, "No text was read from the %s subtitles, so there is no subtitle file for them.", name)
			}
			low += found
		}
	}

	if note := lowConfidenceNote(low); note != "" {
		notes = append(notes, note)
	}
	return strings.Join(notes, " "), nil
}

// takeOut copies one subtitle track out of input as it is, into its own
// file: SUP for a Blu-ray's pictures, SRT for text. It is written under a
// name of ARFABIT's own and renamed into place, never over a file already
// there.
func takeOut(ctx context.Context, job *Job, input string, stream int, path string) error {
	if err := refuseToReplace(path); err != nil {
		return err
	}
	format := strings.TrimPrefix(filepath.Ext(path), ".")
	tmp := filepath.Join(filepath.Dir(path), ".arfabit-"+job.ID+"-"+filepath.Base(path))
	args := []string{"-hide_banner", "-y", "-i", input, "-map", fmt.Sprintf("0:%d", stream), "-c", "copy", "-f", format, tmp}
	if err := ffmpeg.Run(ctx, args, ffmpeg.RunOptions{}); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := refuseToReplace(path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readInto reads one subtitle track of input, the stream numbered stream, into
// the SRT file srt, and lists and keeps on the job the pictures of its
// subtitles of low confidence (§10). A length other than zero trims the lines
// to a clip's. key names the pictures kept. It returns how many subtitles are
// in the SRT, and how many of them have low confidence; with none read, there
// is no SRT.
func (r *Runner) readInto(ctx context.Context, job *Job, input string, stream int, lang string, length time.Duration, srt string, key int, progress func(done, total int)) (read, low int, err error) {
	if r.OCR == nil {
		return 0, 0, errors.New(ocr.NotAvailable)
	}
	name := languageName(lang)

	subtitles, err := subs.ReadTrack(ctx, input, stream)
	if err != nil {
		return 0, 0, err
	}
	result, err := ocr.ReadSubtitles(ctx, r.OCR, subtitles, lang, "", progress)
	if err != nil {
		return 0, 0, err
	}

	cues := result.Cues
	if length > 0 {
		cues = ocr.Trim(cues, length)
	}
	if len(cues) == 0 {
		return 0, 0, nil
	}
	if err := writeSidecar(job, srt, []byte(subs.WriteSRT(cues))); err != nil {
		return 0, 0, err
	}
	job.Sidecars = append(job.Sidecars, srt)
	job.Log.Printf(store.StageOCR, "Read %d %s subtitles into %s.", len(cues), name, filepath.Base(srt))

	for _, l := range result.LowConfidence {
		if length > 0 && l.Start >= length {
			continue
		}
		low++
		entry := store.LowConfidence{Sidecar: srt, Start: l.Start, End: l.End, Text: l.Text, Fast: l.Fast}

		var picture bytes.Buffer
		if err := png.Encode(&picture, subtitles[l.Index].Image); err == nil {
			file := fmt.Sprintf("%d-%05d.png", key, l.Index)
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
	return len(cues), low, nil
}

// lowConfidenceNote says how many subtitles have low confidence, for a job's
// note.
func lowConfidenceNote(low int) string {
	switch {
	case low == 1:
		return "One subtitle has low confidence; the log says which."
	case low > 1:
		return fmt.Sprintf("%d subtitles have low confidence; the log lists them.", low)
	}
	return ""
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

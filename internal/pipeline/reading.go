package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
)

// Reading subtitles is a task of its own (§10): one subtitle track of an
// Original, read into the SRT beside it. That SRT is the one fixes are made
// to, and every film made from the Original gets a copy of it. Reading takes a
// minute or two and little processor, so it takes no processor slot and has
// no limit on how many run at once.

// ReadingRequest is a subtitle track of an Original to read into text.
type ReadingRequest struct {
	Original string
	Title    string
	Year     int

	// Stream is the track's number in the Original.
	Stream int

	// From names the copy that made the Original, when there was one.
	From string
}

// srtFor names the SRT beside an Original for one of its subtitle tracks: the
// Original's own name, then the language, as Plex reads a sidecar. A second
// picture track in the same language, and any after it, is numbered by its
// place among them (".en.2.srt"), in the order the Original holds them.
func srtFor(original string, tracks []Track, stream int) (string, error) {
	var track *Track
	for i := range tracks {
		if tracks[i].Index == stream {
			track = &tracks[i]
		}
	}
	if track == nil || track.Kind != store.KindSubtitle {
		return "", fmt.Errorf("the original has no subtitles at stream %d", stream)
	}
	if track.Codec != pictureSubtitles {
		return "", errors.New("only a Blu-ray's picture subtitles can be read into text")
	}

	place := 0
	for _, t := range tracks {
		if t.Kind == store.KindSubtitle && t.Codec == pictureSubtitles && strings.EqualFold(t.Lang, track.Lang) {
			place++
			if t.Index == stream {
				break
			}
		}
	}

	name := strings.TrimSuffix(original, filepath.Ext(original)) + "." + ocr.Tag(track.Lang)
	if place > 1 {
		name += fmt.Sprintf(".%d", place)
	}
	return name + ".srt", nil
}

// readingKey is how a reading is known while it runs.
func readingKey(original string, stream int) string {
	return fmt.Sprintf("%s#%d", filepath.Clean(original), stream)
}

// StartReading adds an OCR task reading one subtitle track of an Original into
// the SRT beside it. If that track is being read already, it is that task.
func (r *Runner) StartReading(parent context.Context, req ReadingRequest) (*Job, error) {
	if r.OCR == nil {
		return nil, errors.New(ocr.NotAvailable)
	}
	if running := r.reading(req.Original, req.Stream); running != nil {
		return running, nil
	}

	ctx, cancel := context.WithTimeout(parent, time.Minute)
	info, err := ffmpeg.Probe(ctx, req.Original)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("the original could not be read: %w", err)
	}
	tracks := OriginalTracks(info)
	srt, err := srtFor(req.Original, tracks, req.Stream)
	if err != nil {
		return nil, err
	}
	if err := wouldReplace(existingAt(srt)); err != nil {
		return nil, err
	}

	reading := &store.Reading{Stream: req.Stream, SRT: srt}
	for _, t := range tracks {
		if t.Index == req.Stream {
			reading.Lang, reading.Label = t.Lang, t.Label
		}
	}

	title := req.Title
	if title == "" {
		title = filepath.Base(filepath.Dir(req.Original))
	}
	rec := store.NewJob(store.NewJobID(time.Now(), title+" "+ocr.Tag(reading.Lang)+" subtitles"))
	rec.Kind = store.KindOCR
	rec.Stage = store.StageOCR
	rec.Title, rec.Year = title, req.Year
	rec.Original = req.Original
	rec.From = req.From
	rec.Reading = reading

	return r.runReading(parent, rec)
}

// runReading reads an OCR task's track, as a new task or one started again.
func (r *Runner) runReading(parent context.Context, rec *store.Job) (*Job, error) {
	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}
	log.Describe(rec.ID, rec.Title)

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel, read: make(chan struct{})}
	job.File = filepath.Base(rec.Reading.SRT)
	name := languageName(rec.Reading.Lang)
	job.Progress = Progress{Since: time.Now(), Operation: fmt.Sprintf("Reading the %s subtitles", name)}

	r.mu.Lock()
	if r.readings == nil {
		r.readings = map[string]*Job{}
	}
	key := readingKey(rec.Original, rec.Reading.Stream)
	if running := r.readings[key]; running != nil {
		r.mu.Unlock()
		cancel()
		return running, nil
	}
	r.readings[key] = job
	r.mu.Unlock()

	r.begin(job)
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)
		defer func() {
			r.mu.Lock()
			delete(r.readings, key)
			r.mu.Unlock()
			close(job.read)
		}()
		r.read(ctx, job)
	}()
	return job, nil
}

// read is the OCR stage of a reading task.
func (r *Runner) read(ctx context.Context, job *Job) {
	reading := job.Reading
	name := languageName(reading.Lang)
	job.Sidecars, job.LowConfidence = nil, nil
	job.Log.Printf(store.StageOCR, "Reading the %s subtitles of %s into text.", name, filepath.Base(job.Original))

	read, low, err := r.readInto(ctx, job, job.Original, reading.Stream, reading.Lang, 0, reading.SRT, reading.Stream, func(done, total int) {
		job.Progress.Percent = float64(done) / float64(total) * 100
		r.notify(job)
	})

	var language *ocr.LanguageError
	var replace *ReplaceError
	switch {
	case ctx.Err() != nil:
		r.stop(job, "Stopped at your request.", "")
		return
	case errors.As(err, &language):
		r.stop(job, language.Message, "")
		return
	case errors.As(err, &replace):
		r.stop(job, fmt.Sprintf("%s is already there, so ARFABIT stopped rather than replace it. Nothing was removed.", filepath.Base(replace.Path)), replace.Path)
		return
	case err != nil:
		r.stop(job, fmt.Sprintf("ARFABIT did not finish reading the %s subtitles.", name), detailOf(err))
		return
	case read == 0:
		r.stop(job, fmt.Sprintf("No text was read from the %s subtitles, so there is no subtitle file for them.", name), "")
		return
	}

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("The %s subtitles are read, into %s.", name, filepath.Base(reading.SRT))
	if note := lowConfidenceNote(low); note != "" {
		job.Note += " " + note
	}
	job.Log.Printf(store.StageOCR, "%s", job.Note)
	r.save(job)
}

// reading returns the OCR task reading a track, while it runs.
func (r *Runner) reading(original string, stream int) *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readings[readingKey(original, stream)]
}

// waitForReading waits for the OCR task reading a track to end, if one is
// running, and returns it; nil when none is.
func (r *Runner) waitForReading(ctx context.Context, original string, stream int) *Job {
	job := r.reading(original, stream)
	if job == nil {
		return nil
	}
	select {
	case <-job.read:
	case <-ctx.Done():
	}
	return job
}

// readAfterCopy starts an OCR task for each subtitle track chosen on a disc's
// Plan, once its Original is copied: those it lists to read, and those its
// film converts to text, which the film takes from beside the Original.
func (r *Runner) readAfterCopy(ctx context.Context, copied *Job) {
	plan := copied.Plan
	wanted := append([]int(nil), plan.Read...)
	if plan.Convert && plan.Project != nil {
		for _, it := range plan.Project.ItemsOf(store.KindSubtitle) {
			if it.Action == store.ActionConvert {
				wanted = append(wanted, it.Source)
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	if r.OCR == nil {
		copied.Log.Printf(store.StageOCR, "%s", ocr.NotAvailable)
		return
	}

	info, err := ffmpeg.Probe(ctx, copied.Original)
	if err != nil {
		copied.Log.Detail(store.StageOCR, "ARFABIT could not read the original, so its subtitles are not read.", err.Error())
		return
	}
	streams := bindSubtitles(plan.Tracks, OriginalTracks(info))

	started := map[int]bool{}
	for _, index := range wanted {
		stream, ok := streams[index]
		if !ok {
			copied.Log.Printf(store.StageOCR, "Subtitle track %d is not in the original, so it is not read.", index)
			continue
		}
		if started[stream] {
			continue
		}
		started[stream] = true
		if _, err := r.StartReading(context.Background(), ReadingRequest{
			Original: copied.Original, Title: copied.Title, Year: copied.Year, Stream: stream, From: copied.ID,
		}); err != nil {
			copied.Log.Printf(store.StageOCR, "%s.", sentence(err.Error()))
		}
	}
}

// bindSubtitles finds each of a disc's subtitle tracks in its Original, by
// the disc's number for it. MakeMKV keeps a title's subtitle tracks in the
// order the disc lists them, so when both hold as many, the nth is the nth;
// otherwise each is the first of its language not yet taken. Either way the
// language must agree.
func bindSubtitles(disc, original []Track) map[int]int {
	var from, to []Track
	for _, t := range disc {
		if t.Kind == store.KindSubtitle {
			from = append(from, t)
		}
	}
	for _, t := range original {
		if t.Kind == store.KindSubtitle {
			to = append(to, t)
		}
	}

	found := map[int]int{}
	if len(from) == len(to) {
		for i := range from {
			if strings.EqualFold(from[i].Lang, to[i].Lang) {
				found[from[i].Index] = to[i].Index
			}
		}
		return found
	}

	used := map[int]bool{}
	for _, d := range from {
		for _, o := range to {
			if !used[o.Index] && strings.EqualFold(d.Lang, o.Lang) {
				used[o.Index] = true
				found[d.Index] = o.Index
				break
			}
		}
	}
	return found
}

// takeSubtitles puts a copy of the SRT beside the Original, for each subtitle
// line item a film converts to text, beside the film: the Original's SRT as it
// is at the last moment, so a fix made while the film was being made is in
// it. A track still being read is waited for, and one nobody has read is read
// now. A film is never held back by its subtitles: what could not be had is
// said in what it returns, for the note.
func (r *Runner) takeSubtitles(ctx context.Context, job *Job, tracks []Track, out string) string {
	var notes []string
	sidecars := sidecarPaths(job.Project, out)
	for _, it := range job.Project.ItemsOf(store.KindSubtitle) {
		if it.Action != store.ActionConvert {
			continue
		}
		name := languageName(it.Lang)
		srt, err := srtFor(job.Original, tracks, it.Source)
		if err != nil {
			notes = append(notes, fmt.Sprintf("The %s subtitles are not in this film: %s.", name, err))
			continue
		}

		if _, err := os.Stat(srt); err != nil {
			reading := r.waitForReading(ctx, job.Original, it.Source)
			if reading == nil && r.OCR != nil {
				job.Log.Printf(store.StageDeliver, "The %s subtitles have not been read yet, so they are being read now.", name)
				// Its own task, which stopping the film does not stop.
				reading, err = r.StartReading(context.Background(), ReadingRequest{Original: job.Original, Title: job.Title, Year: job.Year, Stream: it.Source, From: job.ID})
				if err == nil {
					reading = r.waitForReading(ctx, job.Original, it.Source)
				}
			}
			if ctx.Err() != nil {
				return ""
			}
			if _, err := os.Stat(srt); err != nil {
				note := fmt.Sprintf("The %s subtitles were not read, so this film has none.", name)
				if reading != nil && reading.Note != "" {
					note += " " + reading.Note
				} else if r.OCR == nil {
					note += " " + ocr.NotAvailable
				}
				job.Log.Printf(store.StageDeliver, "%s", note)
				notes = append(notes, note)
				continue
			}
		}

		data, err := os.ReadFile(srt)
		if err == nil {
			err = writeSidecar(job, sidecars[it.Source], data)
		}
		if err != nil {
			note := fmt.Sprintf("ARFABIT could not put the %s subtitles beside the film.", name)
			job.Log.Detail(store.StageDeliver, note, err.Error())
			notes = append(notes, note)
			continue
		}
		job.Sidecars = append(job.Sidecars, sidecars[it.Source])
		job.Log.Printf(store.StageDeliver, "Copied the %s subtitles from %s.", name, filepath.Base(srt))
	}
	return strings.Join(notes, " ")
}

// readMissing starts an OCR task for each subtitle track a film converts to
// text that has no SRT beside its Original yet, so it is read while the film
// is made rather than after.
func (r *Runner) readMissing(ctx context.Context, original, title string, year int, pkg *store.Project) {
	if r.OCR == nil || !pkg.WholeFilm() {
		return
	}
	var tracks []Track
	for _, it := range pkg.ItemsOf(store.KindSubtitle) {
		if it.Action != store.ActionConvert {
			continue
		}
		if tracks == nil {
			probe, cancel := context.WithTimeout(ctx, time.Minute)
			info, err := ffmpeg.Probe(probe, original)
			cancel()
			if err != nil {
				return
			}
			tracks = OriginalTracks(info)
		}
		srt, err := srtFor(original, tracks, it.Source)
		if err != nil || existingAt(srt) != nil {
			continue
		}
		_, _ = r.StartReading(ctx, ReadingRequest{Original: original, Title: title, Year: year, Stream: it.Source})
	}
}

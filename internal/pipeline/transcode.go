package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/lab"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// LabRequest is one transcode: a copy, a stretch of it, and the profiles to
// try against it.
type LabRequest struct {
	Master string
	Film   string
	Year   int

	// At and Length say which stretch. A Length of zero means the whole film,
	// which is not a test clip at all but a film, and goes to the library.
	At     time.Duration
	Length time.Duration

	// Profiles are the named settings to render. Several at once is the
	// point: comparing means having both to watch.
	Profiles []string

	// LabDir and LibraryDir are where clips and films go respectively.
	LabDir     string
	LibraryDir string
}

// WholeFilm reports whether this produces films rather than clips.
func (r LabRequest) WholeFilm() bool { return r.Length <= 0 }

// StartTranscode renders a copy under one or more profiles.
//
// A stretch of the film becomes test clips to compare; the whole film becomes
// films, one per profile, each an edition in the library. It is the same work
// either way, and treating it as two different features would mean two of
// everything that only differed in how long a piece of film was passed in.
func (r *Runner) StartTranscode(parent context.Context, req LabRequest) (*Job, error) {
	if req.Master == "" {
		return nil, fmt.Errorf("there is no copy to work from")
	}
	if len(req.Profiles) == 0 {
		return nil, fmt.Errorf("nothing was chosen to try")
	}

	film := req.Film
	if film == "" {
		film = filepath.Base(filepath.Dir(req.Master))
	}

	rec := store.NewLabJob(store.NewJobID(time.Now(), film), film)
	rec.Master = req.Master
	rec.Year = req.Year
	if req.WholeFilm() {
		rec.Kind = store.KindConvert
	}

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}
	log.Describe(rec.ID, film)

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel}
	job.Progress = Progress{Since: time.Now(), Operation: "Waiting to start"}

	r.begin(job)
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)
		r.runTranscode(ctx, job, req)
	}()

	return job, nil
}

// runTranscode works through the chosen profiles one at a time.
func (r *Runner) runTranscode(ctx context.Context, job *Job, req LabRequest) {
	if r.Slots != nil {
		if running, _ := r.Slots.Busy(); running > 0 {
			job.Stage = store.StageQueued
			job.Progress = Progress{Since: time.Now(), Operation: "Waiting for a turn"}
			job.Log.Printf(store.StageQueued, "Waiting to start: something else is using the processor.")
			r.save(job)
		}
		if err := r.Slots.Take(ctx); err != nil {
			r.stop(job, "Stopped while waiting to start.", "")
			return
		}
		defer r.Slots.Give()
	}

	job.Stage = store.StageLab
	if req.WholeFilm() {
		job.Stage = store.StagePackage
	}

	what := fmt.Sprintf("a %s clip from %s in", formatDuration(req.Length), formatDuration(req.At))
	if req.WholeFilm() {
		what = "the whole film"
	}
	job.Log.Printf(job.Stage, "Making %s under %d profile%s: %s.",
		what, len(req.Profiles), plural(len(req.Profiles)), joinNames(req.Profiles))
	r.save(job)

	title := meta.Title{Name: job.Title, Year: job.Year}
	var clips []lab.Clip

	for i, name := range req.Profiles {
		if ctx.Err() != nil {
			r.stop(job, "Stopped at your request.", "")
			return
		}

		profile, known := r.Config.ProfileNamed(name)
		if !known {
			job.Log.Printf(job.Stage, "There is no profile called %s, so it has been skipped.", name)
			continue
		}

		job.Progress = Progress{
			Since:     job.Progress.Since,
			Percent:   float64(i) / float64(len(req.Profiles)) * 100,
			Operation: fmt.Sprintf("%s (%d of %d)", profile.Name, i+1, len(req.Profiles)),
		}
		r.save(job)

		clip, err := r.renderProfile(ctx, job, req, profile, title)
		if err != nil {
			job.Log.Detail(job.Stage, fmt.Sprintf("%s did not finish.", profile.Name), err.Error())
			continue
		}
		clips = append(clips, clip)
	}

	if len(clips) == 0 {
		r.stop(job, "Nothing was made.", "")
		return
	}

	if !req.WholeFilm() {
		filmLength := time.Duration(0)
		if info, err := ffmpeg.Probe(ctx, req.Master); err == nil {
			filmLength = time.Duration(info.Duration * float64(time.Second))
		}
		job.Comparison = lab.Compare(clips, req.Length, filmLength)
	}

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%d of %d finished.", len(clips), len(req.Profiles))
	job.Log.Printf(job.Stage, "%s", job.Note)
	r.save(job)
}

// renderProfile makes one output.
func (r *Runner) renderProfile(
	ctx context.Context,
	job *Job,
	req LabRequest,
	profile config.Profile,
	title meta.Title,
) (lab.Clip, error) {
	plan, err := planFromMaster(ctx, req.Master, profile)
	if err != nil {
		return lab.Clip{}, err
	}

	setting := lab.Clip{
		Name:  profile.Name,
		Video: lab.VideoSetting{Copy: plan.VideoCopy, CRF: plan.CRF, Preset: plan.Preset},
	}
	for _, track := range plan.Audio {
		if track.Selected {
			setting.Audio = lab.AudioSetting{
				Copy:        track.Copy,
				Codec:       track.Codec,
				Bitrate:     track.Bitrate,
				Channels:    track.Channels,
				SourceIndex: track.SourceIndex,
			}
			break
		}
	}

	// A whole film is a film: it belongs in the library as an edition, beside
	// any other version of the same film.
	outDir, length := req.LabDir, req.Length
	if req.WholeFilm() {
		outDir = title.LibraryDir(req.LibraryDir)
		length = 0
	}

	start := time.Now()
	clips, err := lab.Run(ctx, lab.Request{
		Master:    req.Master,
		Film:      job.Title,
		At:        req.At,
		Length:    length,
		Settings:  []lab.Clip{setting},
		OutputDir: outDir,
		WholeFilm: req.WholeFilm(),
		FilmTitle: title,
	})
	if err != nil {
		return lab.Clip{}, err
	}
	if len(clips) == 0 {
		return lab.Clip{}, fmt.Errorf("nothing came out")
	}

	clip := clips[0]
	if clip.Problem != "" {
		return lab.Clip{}, fmt.Errorf("%s", clip.Problem)
	}

	job.Log.Printf(job.Stage, "%s — %s, took %s.",
		profile.Name, HumanBytes(clip.Size), time.Since(start).Round(time.Second))

	return clip, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}

	out := ""
	for i, name := range names {
		switch {
		case i == 0:
			out = name
		case i == len(names)-1:
			out += " and " + name
		default:
			out += ", " + name
		}
	}
	return out
}

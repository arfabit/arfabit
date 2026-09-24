package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/lab"
	"github.com/arfabit/arfabit/internal/store"
)

// LabRequest is one set of test clips to render.
type LabRequest struct {
	Master    string
	Film      string
	At        time.Duration
	Length    time.Duration
	Settings  []lab.Clip
	OutputDir string
}

// StartLab renders test clips as a job in the queue.
//
// A lab run is work like any other: it takes a turn at the processor, it has a
// log, it can be stopped, and it appears in the queue beside the discs. The
// alternative — a separate mechanism with its own progress and its own rules —
// would mean two of everything and a page that tells two stories.
func (r *Runner) StartLab(parent context.Context, req LabRequest) (*Job, error) {
	if req.Master == "" {
		return nil, fmt.Errorf("the lab needs a copy to work from")
	}
	if len(req.Settings) == 0 {
		return nil, fmt.Errorf("the lab needs something to try")
	}

	film := req.Film
	if film == "" {
		film = filepath.Base(req.Master)
	}

	rec := store.NewLabJob(store.NewJobID(time.Now(), "lab-"+film), film)

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
	job.Progress = Progress{Since: time.Now(), Operation: "Making test clips"}

	r.begin(job)
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)
		r.runLab(ctx, job, req)
	}()

	return job, nil
}

// runLab renders the clips, waiting its turn first.
func (r *Runner) runLab(ctx context.Context, job *Job, req LabRequest) {
	log := job.Log

	if r.Slots != nil {
		if running, _ := r.Slots.Busy(); running > 0 {
			job.Stage = store.StageQueued
			job.Progress = Progress{Since: time.Now(), Operation: "Waiting for a turn"}
			log.Printf(store.StageQueued, "Waiting to start: something else is using the processor.")
			r.save(job)
		}

		if err := r.Slots.Take(ctx); err != nil {
			r.stop(job, "Stopped while waiting to start.", "")
			return
		}
		defer r.Slots.Give()
	}

	job.Stage = store.StageLab
	job.Progress = Progress{Since: time.Now(), Operation: "Making test clips"}
	log.Printf(store.StageLab, "Making %d clips from %s, %s in.",
		len(req.Settings), filepath.Base(req.Master), formatDuration(req.At))
	r.save(job)

	done := 0
	clips, err := lab.Run(ctx, lab.Request{
		Master:    req.Master,
		Film:      req.Film,
		At:        req.At,
		Length:    req.Length,
		Settings:  req.Settings,
		OutputDir: req.OutputDir,
		OnClip: func(clip lab.Clip) {
			done++
			job.Progress = Progress{
				Since:     job.Progress.Since,
				Percent:   float64(done) / float64(len(req.Settings)) * 100,
				Operation: fmt.Sprintf("Finished %s", clip.Name),
			}

			if clip.Problem != "" {
				log.Detail(store.StageLab, fmt.Sprintf("%s did not finish.", clip.Name), clip.Problem)
			} else {
				log.Printf(store.StageLab, "%s — %s, took %s.",
					clip.Name, HumanBytes(clip.Size), clip.Took.Round(time.Second))
			}
			r.save(job)
		},
	})
	if err != nil {
		r.stop(job, "The test clips could not be made.", err.Error())
		return
	}

	// What the clips mean for a whole film is the only reason to make them.
	filmLength := time.Duration(0)
	if info, probeErr := ffmpeg.Probe(ctx, req.Master); probeErr == nil {
		filmLength = time.Duration(info.Duration * float64(time.Second))
	}
	job.Comparison = lab.Compare(clips, req.Length, filmLength)

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%d test clips are ready in %s.", len(clips), req.OutputDir)
	log.Printf(store.StageLab, "%s", job.Note)
	r.save(job)
}

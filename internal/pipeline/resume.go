package pipeline

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// Reconcile settles jobs left behind by a restart.
//
// Job records are written to disk as they change, so a restart finds jobs
// still marked as running when nothing is. They cannot be running — this
// process has only just started — so saying so is the first honest thing to
// do, and offering to pick up whatever can be picked up is the second.
//
// Nothing is resumed automatically. Starting hours of work because a program
// was restarted is not a decision to make on somebody's behalf.
func (r *Runner) Reconcile() ([]*store.Job, error) {
	jobs, err := r.Store.Jobs()
	if err != nil {
		return nil, err
	}

	var interrupted []*store.Job

	for _, job := range jobs {
		if job.State != store.StateRunning {
			continue
		}

		job.State = store.StateStopped
		job.Note = interruptedNote(job)

		if err := r.Store.SaveJob(job); err != nil {
			continue
		}
		interrupted = append(interrupted, job)
	}

	return interrupted, nil
}

// interruptedNote says what became of a job and what can be done about it.
func interruptedNote(job *store.Job) string {
	switch {
	case job.Kind == store.KindDisc && job.Stage == store.StageRip:
		// A half-copied disc cannot be picked up where it left off: the copy
		// stops mid-file and MakeMKV has no way back into it.
		return "ARFABIT was restarted while this disc was being copied. The copy is unfinished, so the disc needs reading again."

	case job.Kind == store.KindDisc && (job.Stage == store.StageScan || job.Stage == store.StagePlan):
		return "ARFABIT was restarted while this disc was being read. Nothing was copied, so read it again when you like."

	case job.From != "" && job.Original == "":
		return "ARFABIT was restarted before this disc was copied, so its transcode never started. Read the disc again to plan it."

	case job.Kind == store.KindOCR:
		return "ARFABIT was restarted while these subtitles were being read. Nothing was written, so they can be read again."

	case job.Original != "":
		// Everything after the disc works from the copy, and the copy is
		// still there.
		return "ARFABIT was restarted while this was being converted. The copy is untouched, so it can be started again."

	default:
		return "ARFABIT was restarted while this was running."
	}
}

// Resumable reports whether a stopped job can simply be started again.
//
// A job can be run again when its copy is still there and it knows what it was
// going to do with it: a disc job that got past the copy has its Plan, and a
// Transcode has its Plans. A disc still being copied cannot, because its copy
// stopped mid-file.
func Resumable(job *store.Job) bool {
	if job.Original == "" {
		return false
	}

	switch {
	case job.Kind == store.KindOCR:
		// Read again, as long as nothing has been written where it reads to.
		if job.Reading == nil || existingAt(job.Reading.SRT) != nil {
			return false
		}
	case job.Project != nil:
		// A package has its line items, which is all it needs.
	case job.Kind == store.KindDisc:
		// Disc jobs from before a rip and its transcode were separate jobs
		// carried their own conversion, and can still be picked up.
		if job.Plan == nil || !job.Plan.Convert {
			return false
		}
		switch job.Stage {
		case store.StageEject, store.StageOCR, store.StageQueued, store.StagePackage, store.StageDeliver:
		default:
			return false
		}
	case job.Transcode != nil:
		// Made by the Transcode form packages replaced. Its copy is still
		// there to make a package from.
		return false
	default:
		// A transcode planned with its disc has that disc's Plan.
		if job.Plan == nil {
			return false
		}
	}

	_, err := os.Stat(job.Original)
	return err == nil
}

// ResumeJob runs a stopped job again from its copy, as it was planned.
//
// The job keeps its record, its log and its Plans. Nothing is looked up again:
// the blueprints it started from may have changed or gone since, and that does
// not reach a job that already has its settings (§8).
func (r *Runner) ResumeJob(id string) (*Job, error) {
	rec, err := r.Store.LoadJob(id)
	if err != nil {
		return nil, fmt.Errorf("there is nothing here called %s", id)
	}
	for _, active := range r.Active() {
		if active.ID == id {
			return nil, fmt.Errorf("%s is already running", rec.Title)
		}
	}
	if rec.State != store.StateStopped || !Resumable(rec) {
		return nil, fmt.Errorf("%s cannot be picked up where it left off", rec.Title)
	}

	if rec.Kind == store.KindOCR {
		rec.State, rec.Note, rec.Detail = store.StateRunning, "", ""
		return r.runReading(context.Background(), rec)
	}

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}
	log.Describe(rec.ID, rec.Title)

	rec.State = store.StateRunning
	rec.Note = ""
	rec.Detail = ""

	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{Job: rec, Log: log, cancel: cancel}
	job.Progress = Progress{Since: time.Now(), Operation: "Waiting to start"}
	log.Printf(rec.Stage, "Starting again from the copy, with the settings it had before.")

	r.begin(job)
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)

		if rec.Project != nil {
			// Started again now, with nothing to hold back for: whoever
			// pressed Start again meant it.
			r.runPackage(ctx, job, r.configuredDirs(), 0)
			return
		}
		_ = r.convert(ctx, job, meta.Title{Name: rec.Title, Year: rec.Year})
	}()

	return job, nil
}

// InterruptedAt is when a job was last heard from, for the page to show.
func InterruptedAt(job *store.Job) time.Time { return job.Updated }

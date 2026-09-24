package pipeline

import (
	"context"
	"fmt"
	"os"
	"time"

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

	case job.Master != "":
		// Everything after the disc works from the copy, and the copy is
		// still there.
		return "ARFABIT was restarted while this was being converted. The copy is untouched, so it can be started again."

	default:
		return "ARFABIT was restarted while this was running."
	}
}

// Resumable reports whether an interrupted job can simply be started again.
//
// Anything working from a copy can, because the copy is still there. A disc
// cannot: its copy stopped mid-file.
func Resumable(job *store.Job) bool {
	if job.Kind == store.KindDisc {
		return false
	}
	if job.Master == "" {
		return false
	}

	_, err := os.Stat(job.Master)
	return err == nil
}

// ResumeJob starts an interrupted job again from its copy.
func (r *Runner) ResumeJob(id string) (*Job, error) {
	previous, err := r.Store.LoadJob(id)
	if err != nil {
		return nil, fmt.Errorf("there is nothing here called %s", id)
	}
	if !Resumable(previous) {
		return nil, fmt.Errorf("%s cannot be picked up where it left off", previous.Title)
	}

	profiles := []string{}
	if previous.Plan != nil && previous.Plan.Profile != "" {
		profiles = append(profiles, previous.Plan.Profile)
	}

	return r.StartTranscode(context.Background(), LabRequest{
		Master:     previous.Master,
		Film:       previous.Title,
		Year:       previous.Year,
		Length:     0, // the whole of it, as before
		Profiles:   profiles,
		Lookup:     r.Config.ProfileNamed,
		LabDir:     r.Config.Paths.Lab,
		LibraryDir: r.Config.Paths.Library,
	})
}

// InterruptedAt is when a job was last heard from, for the page to show.
func InterruptedAt(job *store.Job) time.Time { return job.Updated }

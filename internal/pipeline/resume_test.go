package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/store"
)

// A job cannot be running when the program has only just started, so saying so
// is the first honest thing to do.
func TestReconcileSettlesJobsLeftRunning(t *testing.T) {
	st := testStore(t)
	r := &Runner{Store: st, Calibration: NewCalibration()}

	running := store.NewJob("was-running")
	running.Title = "Crime 101"
	running.Stage = store.StageRip
	if err := st.SaveJob(running); err != nil {
		t.Fatal(err)
	}

	finished := store.NewJob("was-finished")
	finished.State = store.StateDone
	if err := st.SaveJob(finished); err != nil {
		t.Fatal(err)
	}

	interrupted, err := r.Reconcile()
	if err != nil {
		t.Fatal(err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("settled %d jobs, want the one that was running", len(interrupted))
	}

	reloaded, err := st.LoadJob("was-running")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.State != store.StateStopped {
		t.Errorf("State = %q, want stopped", reloaded.State)
	}
	if !strings.Contains(reloaded.Note, "restarted") {
		t.Errorf("Note = %q; it should say what happened", reloaded.Note)
	}

	// A finished job is left exactly as it was.
	if done, _ := st.LoadJob("was-finished"); done.State != store.StateDone {
		t.Errorf("a finished job was altered: %q", done.State)
	}
}

// A half-copied disc cannot be picked up where it left off, and the note says
// what to do instead. Anything working from a copy can be.
func TestWhatCanBePickedUp(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.mkv")
	if err := os.WriteFile(original, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ripping := &store.Job{Kind: store.KindDisc, Stage: store.StageRip, Title: "Crime 101"}
	if Resumable(ripping) {
		t.Error("a half-copied disc was offered as resumable")
	}
	if !strings.Contains(interruptedNote(ripping), "reading again") {
		t.Errorf("the note does not say what to do: %q", interruptedNote(ripping))
	}

	planned := &store.Project{Items: []store.Item{{Kind: store.KindVideo, Action: store.ActionCopy}}}

	converting := &store.Job{Kind: store.KindConvert, Stage: store.StagePackage, Original: original, Project: planned}
	if !Resumable(converting) {
		t.Error("a conversion from a copy that is still there was not offered")
	}
	if !strings.Contains(interruptedNote(converting), "started again") {
		t.Errorf("the note does not offer to start it again: %q", interruptedNote(converting))
	}

	// A copy that has since been removed cannot be worked from.
	missing := &store.Job{Kind: store.KindConvert, Original: filepath.Join(dir, "gone.mkv"), Project: planned}
	if Resumable(missing) {
		t.Error("a conversion whose copy is gone was offered as resumable")
	}

	// Without its line items there is nothing to run again.
	unplanned := &store.Job{Kind: store.KindConvert, Original: original}
	if Resumable(unplanned) {
		t.Error("a conversion with nothing planned was offered as resumable")
	}

	// A copy that finished is done: its film is a task of its own.
	copied := &store.Job{Kind: store.KindDisc, Stage: store.StageEject, Original: original,
		Plan: &store.Plan{Convert: true}}
	if Resumable(copied) {
		t.Error("a finished copy was offered to start again")
	}
}

// Running a job again runs its own line items. The blueprint they came from
// may have been changed or removed since, and that does not reach it (§8).
func TestResumeKeepsThePlan(t *testing.T) {
	st := testStore(t)
	cfg := config.Defaults()
	cfg.Paths.Library = t.TempDir()
	r := &Runner{Config: cfg, Store: st, Calibration: NewCalibration(), Slots: NewSlots(1)}

	original := filepath.Join(t.TempDir(), "original.mkv")
	if err := os.WriteFile(original, []byte("not really a film"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := store.NewJob("interrupted-film")
	rec.Kind = store.KindConvert
	rec.Title = "Crime 101"
	rec.State = store.StateStopped
	rec.Stage = store.StagePackage
	rec.Original = original
	rec.Project = &store.Project{Blueprint: "Removed Since", Items: []store.Item{
		{Kind: store.KindVideo, Action: store.ActionConvert, To: "hevc", CRF: 17, Preset: "slower"}}}
	if err := st.SaveJob(rec); err != nil {
		t.Fatal(err)
	}

	if _, err := r.ResumeJob(rec.ID); err != nil {
		t.Fatalf("a job whose blueprint no longer exists was refused: %v", err)
	}
	waitUntilIdle(t, r)

	after, err := st.LoadJob(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v := after.Project.Items[0]; after.Project.Blueprint != "Removed Since" || v.CRF != 17 || v.Preset != "slower" {
		t.Errorf("the line items changed on resume: %+v", after.Project)
	}

	// It ran, as the same job, rather than being replaced by a new one.
	jobs, _ := st.Jobs()
	if len(jobs) != 1 {
		t.Errorf("%d job records, want the one", len(jobs))
	}
	data, _ := os.ReadFile(st.LogPath(rec.ID))
	if !strings.Contains(string(data), "settings it had before") {
		t.Errorf("the log does not say it started again:\n%s", data)
	}
}

// Nothing is resumed on its own: starting hours of work because a program was
// restarted is not a decision to make on somebody's behalf.
func TestReconcileStartsNothing(t *testing.T) {
	st := testStore(t)
	r := &Runner{Store: st, Calibration: NewCalibration(), Slots: NewSlots(1)}

	job := store.NewJob("interrupted")
	job.Kind = store.KindConvert
	job.Original = "/somewhere/original.mkv"
	if err := st.SaveJob(job); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(r.Active()) != 0 {
		t.Errorf("%d jobs were started by a restart", len(r.Active()))
	}
}

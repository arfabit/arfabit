package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	master := filepath.Join(dir, "master.mkv")
	if err := os.WriteFile(master, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ripping := &store.Job{Kind: store.KindDisc, Stage: store.StageRip, Title: "Crime 101"}
	if Resumable(ripping) {
		t.Error("a half-copied disc was offered as resumable")
	}
	if !strings.Contains(interruptedNote(ripping), "reading again") {
		t.Errorf("the note does not say what to do: %q", interruptedNote(ripping))
	}

	converting := &store.Job{Kind: store.KindConvert, Stage: store.StagePackage, Master: master}
	if !Resumable(converting) {
		t.Error("a conversion from a copy that is still there was not offered")
	}
	if !strings.Contains(interruptedNote(converting), "started again") {
		t.Errorf("the note does not offer to start it again: %q", interruptedNote(converting))
	}

	// A copy that has since been removed cannot be worked from.
	missing := &store.Job{Kind: store.KindConvert, Master: filepath.Join(dir, "gone.mkv")}
	if Resumable(missing) {
		t.Error("a conversion whose copy is gone was offered as resumable")
	}
}

// Nothing is resumed on its own: starting hours of work because a program was
// restarted is not a decision to make on somebody's behalf.
func TestReconcileStartsNothing(t *testing.T) {
	st := testStore(t)
	r := &Runner{Store: st, Calibration: NewCalibration(), Slots: NewSlots(1)}

	job := store.NewJob("interrupted")
	job.Kind = store.KindConvert
	job.Master = "/somewhere/master.mkv"
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

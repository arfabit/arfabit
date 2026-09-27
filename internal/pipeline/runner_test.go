package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/store"
)

// Repeated SCSI timeouts mean the drive gave up on the disc, which is a
// physical problem a person can do something about.
func TestReadFailureExplainsATimeout(t *testing.T) {
	err := &makemkv.Error{
		Op: "scan",
		Messages: []makemkv.Message{
			{Code: 3007, Text: "Error 'Scsi error - HARDWARE ERROR:TIMEOUT ON LOGICAL UNIT' occurred while reading ..."},
		},
	}

	note := readFailureNote(err)
	if !strings.Contains(note, "dirty or scratched") {
		t.Errorf("the note does not suggest anything to try: %q", note)
	}
	if !strings.Contains(note, "power") {
		t.Errorf("the note does not mention power, a common cause with bus-powered drives: %q", note)
	}
}

// Anything unrecognised gets the plain statement, never a guessed cause.
func TestReadFailureDoesNotGuess(t *testing.T) {
	note := readFailureNote(errors.New("something nobody has seen before"))

	if note != "ARFABIT did not finish reading this disc." {
		t.Errorf("a cause was invented for an unknown failure: %q", note)
	}
}

// Stop with nothing running must still settle the job rather than leave it
// looking as though it were still going.
func TestStopWithNothingRunning(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}
	r.SetCurrentForTest(&Job{Job: &store.Job{ID: "idle", State: store.StateWaiting}})

	r.Stop()

	if got := r.Current().State; got != store.StateStopped {
		t.Errorf("State = %q, want stopped", got)
	}
}

// Stopping a scan cancels it, and says so honestly: the drive finishes what it
// is doing first.
func TestStopCancelsAndSaysSo(t *testing.T) {
	logFile := t.TempDir() + "/job.txt"
	log, err := NewLog(logFile, nil)
	if err != nil {
		t.Fatal(err)
	}

	var cancelled bool
	job := &Job{
		Job:    &store.Job{ID: "scanning", State: store.StateRunning, Stage: store.StageScan},
		Log:    log,
		cancel: func() { cancelled = true },
	}

	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}
	r.SetCurrentForTest(job)
	r.Stop()

	if !cancelled {
		t.Error("the scan was not cancelled")
	}
	if job.State != store.StateStopped {
		t.Errorf("State = %q, want stopped", job.State)
	}

	var said bool
	for _, e := range log.Entries() {
		if strings.Contains(e.Text, "take a moment") {
			said = true
		}
	}
	if !said {
		t.Error("the message does not admit that stopping is not instant")
	}
}

// waitUntilIdle waits for every job to finish, so nothing is still writing
// when a test's temporary directory is removed.
func waitUntilIdle(t *testing.T, r *Runner) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.Active()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Log("a job was still running when the test ended")
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(t.TempDir(), "test-node")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A Plan nobody acted on should not sit in the list as though it were still
// expecting an answer. Reading a disc again is how somebody changes their mind.
func TestScanRetiresAnUnstartedPlan(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}

	abandoned := &Job{Job: &store.Job{
		ID:    "abandoned",
		State: store.StateWaiting,
		Stage: store.StagePlan,
	}}
	r.SetCurrentForTest(abandoned)

	r.retirePreviousScan()

	if abandoned.State != store.StateStopped {
		t.Errorf("State = %q, want stopped", abandoned.State)
	}
	if abandoned.Note != "Not started." {
		t.Errorf("Note = %q", abandoned.Note)
	}
}

// A job that is actually running must not be retired by a new scan.
func TestScanLeavesRunningJobsAlone(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}

	running := &Job{Job: &store.Job{ID: "busy", State: store.StateRunning, Stage: store.StageRip}}
	r.SetCurrentForTest(running)

	r.retirePreviousScan()

	if running.State != store.StateRunning {
		t.Errorf("State = %q; a running job was retired", running.State)
	}
}

// Only reading and copying need the drive. A disc being converted finished
// with the drive when it was ejected, so the next disc can go straight in —
// which is the whole point of ejecting early.
func TestDriveIsFreeWhileAFilmConverts(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}
	r.SetCurrentForTest(&Job{Job: &store.Job{
		ID:    "converting",
		Title: "Crime 101",
		State: store.StateRunning,
		Stage: store.StagePackage,
	}})

	if busy := r.DriveIsBusy(); busy != nil {
		t.Errorf("the drive is reported busy with %s, which is only being converted", busy.Title)
	}
}

// While a disc is actually being copied, the drive is not available.
func TestDriveIsBusyWhileCopying(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}
	r.SetCurrentForTest(&Job{Job: &store.Job{
		ID:    "ripping",
		Title: "In the Grey",
		State: store.StateRunning,
		Stage: store.StageRip,
	}})

	busy := r.DriveIsBusy()
	if busy == nil {
		t.Fatal("the drive is reported free while a disc is being copied")
	}
	if busy.Title != "In the Grey" {
		t.Errorf("the wrong job was named: %q", busy.Title)
	}
}

// Scanning a second disc while the first is copying must be refused, and the
// message should say what the drive is doing and that it will free up.
func TestScanRefusesWhileTheDriveIsBusy(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}
	r.SetCurrentForTest(&Job{Job: &store.Job{
		ID:    "ripping",
		Title: "Crime 101",
		State: store.StateRunning,
		Stage: store.StageRip,
	}})

	_, err := r.Scan(context.Background(), disc.Drive{Index: 0})
	if err == nil {
		t.Fatal("a second disc was accepted while one was being copied")
	}
	if !strings.Contains(err.Error(), "Crime 101") {
		t.Errorf("the message does not say what the drive is doing: %q", err)
	}
	if !strings.Contains(err.Error(), "free") {
		t.Errorf("the message does not say the wait is temporary: %q", err)
	}
}

// Several films can be converting at once while the drive works through more
// discs.
func TestSeveralJobsCanBeActive(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration()}

	for _, title := range []string{"One", "Two", "Three"} {
		r.begin(&Job{Job: &store.Job{
			ID:    title,
			Title: title,
			State: store.StateRunning,
			Stage: store.StagePackage,
		}})
	}

	if got := len(r.Active()); got != 3 {
		t.Errorf("got %d active jobs, want 3", got)
	}
	if r.DriveIsBusy() != nil {
		t.Error("the drive is busy despite every job being past the disc")
	}
}

// The Plan records the disc's stream numbers, which are not the original's:
// MakeMKV keeps only some streams and renumbers what it keeps. Matching by
// language alone resolved every English track to the same stream, so a track
// marked "copy" copied a different track than the one planned.
// Copying is decided from what the original holds, not from what the Plan said:
// the original is what gets muxed.
// A stereo downmix shares its source with the track it came from, and must
// actually be downmixed.
func testJob(t *testing.T) *Job {
	t.Helper()
	log, err := NewLog(t.TempDir()+"/job.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Job{Job: &store.Job{ID: "test"}, Log: log}
}

// A disc waiting for the processor must not be holding the drive: that is the
// whole reason ejecting happens early.
func TestQueuedJobDoesNotHoldTheDrive(t *testing.T) {
	r := &Runner{Store: testStore(t), Calibration: NewCalibration(), Slots: NewSlots(1)}
	r.SetCurrentForTest(&Job{Job: &store.Job{
		ID:    "queued",
		Title: "Crime 101",
		State: store.StateRunning,
		Stage: store.StageQueued,
	}})

	if busy := r.DriveIsBusy(); busy != nil {
		t.Errorf("the drive is held by %s, which is only waiting for the processor", busy.Title)
	}
}

// Waiting its turn is a stage of its own, because waiting and working look
// identical otherwise.
func TestQueuedStageIsNamedPlainly(t *testing.T) {
	if got := stageWords(store.StageQueued); got != "Waiting its turn" {
		t.Errorf("stageWords(QUEUED) = %q", got)
	}
}

// Stopping at the copy is the fast way through a stack of discs: the copy is
// the only part that needs the drive.

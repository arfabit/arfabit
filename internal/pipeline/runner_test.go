package pipeline

import (
	"errors"
	"strings"
	"testing"

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

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(t.TempDir(), "test-node")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

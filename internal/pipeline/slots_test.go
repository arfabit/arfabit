package pipeline

import (
	"context"
	"testing"
	"time"
)

// One at a time is the default, because x265 already uses every core: a second
// encode does not finish sooner, it makes both later.
func TestOneSlotAdmitsOneAtATime(t *testing.T) {
	slots := NewSlots(1)

	if err := slots.Take(context.Background(), Ticket{}); err != nil {
		t.Fatal(err)
	}

	blocked := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		blocked <- slots.Take(ctx, Ticket{})
	}()

	if err := <-blocked; err == nil {
		t.Error("a second conversion started while one was already running")
	}

	running, _ := slots.Busy()
	if running != 1 {
		t.Errorf("running = %d, want 1", running)
	}

	slots.Give()
	if err := slots.Take(context.Background(), Ticket{}); err != nil {
		t.Errorf("the freed slot was not handed on: %v", err)
	}
}

// A job stopped while waiting its turn must stop, not start later out of turn.
func TestWaitingCanBeAbandoned(t *testing.T) {
	slots := NewSlots(1)
	if err := slots.Take(context.Background(), Ticket{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- slots.Take(ctx, Ticket{}) }()

	// Let it settle into the queue before abandoning it.
	time.Sleep(20 * time.Millisecond)
	if _, waiting := slots.Busy(); waiting != 1 {
		t.Errorf("waiting = %d, want 1", waiting)
	}

	cancel()
	if err := <-result; err == nil {
		t.Error("an abandoned wait reported success")
	}
}

func TestSeveralSlots(t *testing.T) {
	slots := NewSlots(3)

	for i := 0; i < 3; i++ {
		if err := slots.Take(context.Background(), Ticket{}); err != nil {
			t.Fatalf("slot %d was refused: %v", i, err)
		}
	}

	if running, _ := slots.Busy(); running != 3 {
		t.Errorf("running = %d, want 3", running)
	}
	if slots.Count() != 3 {
		t.Errorf("Count = %d, want 3", slots.Count())
	}
}

// Nothing is gained by allowing none.
func TestZeroSlotsBecomesOne(t *testing.T) {
	if got := NewSlots(0).Count(); got != 1 {
		t.Errorf("NewSlots(0) allows %d at once, want 1", got)
	}
}

// The line is the order things start in, and moving a job in it changes that.
func TestLineCanBeRearranged(t *testing.T) {
	slots := NewSlots(1)
	if err := slots.Take(context.Background(), Ticket{ID: "running"}); err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 2)
	for _, id := range []string{"first", "second"} {
		go func() {
			if err := slots.Take(context.Background(), Ticket{ID: id}); err == nil {
				started <- id
			}
		}()
		waitForLine(t, slots, id)
	}

	if !slots.Move("second", 0) {
		t.Fatal("second was not found in the line")
	}
	if got := slots.Line(); len(got) != 2 || got[0] != "second" || got[1] != "first" {
		t.Fatalf("line = %v, want [second first]", got)
	}

	slots.Give()
	if got := <-started; got != "second" {
		t.Errorf("%s started first, want second", got)
	}
	slots.Give()
	if got := <-started; got != "first" {
		t.Errorf("%s started second, want first", got)
	}
}

// A held job does not start until its hold is over, even with a slot free,
// and nothing behind it jumps ahead in the meantime.
func TestHeldJobWaitsAndKeepsItsPlace(t *testing.T) {
	slots := NewSlots(1)
	hold := 150 * time.Millisecond

	began := time.Now()
	started := make(chan string, 2)
	go func() {
		if err := slots.Take(context.Background(), Ticket{ID: "held", NotBefore: began.Add(hold)}); err == nil {
			started <- "held"
		}
	}()
	waitForLine(t, slots, "held")

	go func() {
		if err := slots.Take(context.Background(), Ticket{ID: "behind"}); err == nil {
			started <- "behind"
		}
	}()

	if got := <-started; got != "held" {
		t.Errorf("%s started first, want held", got)
	}
	if waited := time.Since(began); waited < hold {
		t.Errorf("held job started after %v, before its hold of %v", waited, hold)
	}
	slots.Give()
	<-started
}

// Waiting is said once, and only when a job could start but for a busy slot.
func TestWaitingIsSaidOnceWhenBusy(t *testing.T) {
	slots := NewSlots(1)
	if err := slots.Take(context.Background(), Ticket{ID: "running"}); err != nil {
		t.Fatal(err)
	}

	said := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() {
		done <- slots.Take(context.Background(), Ticket{ID: "next", Waiting: func() { said <- struct{}{} }})
	}()
	waitForLine(t, slots, "next")
	slots.Move("next", 0) // wakes it without freeing anything
	slots.Give()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 {
		t.Errorf("waiting was said %d times, want 1", len(said))
	}

	quiet := make(chan struct{}, 1)
	slots.Give()
	if err := slots.Take(context.Background(), Ticket{Waiting: func() { quiet <- struct{}{} }}); err != nil {
		t.Fatal(err)
	}
	if len(quiet) != 0 {
		t.Error("a job that started straight away said it was waiting")
	}
}

func waitForLine(t *testing.T, slots *Slots, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, waiting := range slots.Line() {
			if waiting == id {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s never joined the line", id)
}

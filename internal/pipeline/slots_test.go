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

	if err := slots.Take(context.Background()); err != nil {
		t.Fatal(err)
	}

	blocked := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		blocked <- slots.Take(ctx)
	}()

	if err := <-blocked; err == nil {
		t.Error("a second conversion started while one was already running")
	}

	running, _ := slots.Busy()
	if running != 1 {
		t.Errorf("running = %d, want 1", running)
	}

	slots.Give()
	if err := slots.Take(context.Background()); err != nil {
		t.Errorf("the freed slot was not handed on: %v", err)
	}
}

// A job stopped while waiting its turn must stop, not start later out of turn.
func TestWaitingCanBeAbandoned(t *testing.T) {
	slots := NewSlots(1)
	if err := slots.Take(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- slots.Take(ctx) }()

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
		if err := slots.Take(context.Background()); err != nil {
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

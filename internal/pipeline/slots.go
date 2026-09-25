package pipeline

import (
	"context"
	"sync"
	"time"
)

// Slots limits how much converting happens at once.
//
// Packaging a film and rendering a lab clip are the same work: x265 on this
// machine's processor, which already uses every core it can find. Running two
// at once finishes both later than running them one after the other would, and
// makes a nonsense of both estimates. So they share a small number of slots,
// and whatever cannot start waits.
//
// Whatever waits, waits in a line, and the line can be rearranged from the
// page. Only the front of the line is ever given a slot, so the order shown is
// the order things start in.
//
// Reading discs is not limited here. A drive can only do one thing at a time
// and that is handled where the drive is, which is the whole point: discs keep
// going in while films convert behind them.
type Slots struct {
	count int

	mu      sync.Mutex
	running int
	line    []*waiter

	// changed is closed and replaced whenever anything happens that could let
	// a waiter through: a slot coming free, the line moving, a waiter leaving.
	changed chan struct{}
}

// waiter is one job in the line.
type waiter struct {
	id string

	// notBefore holds a job back for a moment after it joins, so the line can
	// still be rearranged before anything starts.
	notBefore time.Time
}

// NewSlots makes a limiter. A count below one is treated as one: nothing is
// gained by allowing none.
func NewSlots(count int) *Slots {
	if count < 1 {
		count = 1
	}
	return &Slots{count: count, changed: make(chan struct{})}
}

// Ticket is a job asking for a slot.
type Ticket struct {
	// ID names the job, so the page can move it in the line.
	ID string

	// NotBefore is the earliest the job may start. Zero means straight away.
	NotBefore time.Time

	// Waiting, if set, is called once if the job is free to start but every
	// slot is taken, so it can say it is waiting its turn.
	Waiting func()
}

// Take joins the line and waits for a slot.
//
// Returns the context's error if the wait is abandoned, so a job stopped while
// queued stops rather than starting later out of turn.
func (s *Slots) Take(ctx context.Context, t Ticket) error {
	w := &waiter{id: t.ID, notBefore: t.NotBefore}

	s.mu.Lock()
	s.line = append(s.line, w)
	s.mu.Unlock()

	said := false
	for {
		s.mu.Lock()
		held := time.Until(w.notBefore)
		if held <= 0 && s.running < s.count && s.line[0] == w {
			s.line = s.line[1:]
			s.running++
			s.broadcastLocked()
			s.mu.Unlock()
			return nil
		}
		changed := s.changed
		s.mu.Unlock()

		if held <= 0 && !said && t.Waiting != nil {
			said = true
			t.Waiting()
		}

		var timer *time.Timer
		var ready <-chan time.Time
		if held > 0 {
			timer = time.NewTimer(held)
			ready = timer.C
		}

		select {
		case <-changed:
		case <-ready:
		case <-ctx.Done():
			s.mu.Lock()
			s.removeLocked(w)
			s.broadcastLocked()
			s.mu.Unlock()
			if timer != nil {
				timer.Stop()
			}
			return ctx.Err()
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// Give returns a slot.
func (s *Slots) Give() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running > 0 {
		s.running--
	}
	s.broadcastLocked()
}

// Move puts a waiting job at a new place in the line, counting from zero at
// the front. A place past either end is taken to mean that end. It reports
// whether the job was in the line at all.
func (s *Slots) Move(id string, to int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	from := -1
	for i, w := range s.line {
		if w.id == id {
			from = i
			break
		}
	}
	if from < 0 {
		return false
	}

	to = max(0, min(to, len(s.line)-1))
	w := s.line[from]
	s.line = append(s.line[:from], s.line[from+1:]...)
	s.line = append(s.line[:to], append([]*waiter{w}, s.line[to:]...)...)

	s.broadcastLocked()
	return true
}

// Line is the ids of the jobs waiting, front first.
func (s *Slots) Line() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, len(s.line))
	for i, w := range s.line {
		ids[i] = w.id
	}
	return ids
}

// Busy reports how many are converting and how many are waiting their turn.
func (s *Slots) Busy() (running, waiting int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, len(s.line)
}

// Count is how many may convert at once.
func (s *Slots) Count() int { return s.count }

func (s *Slots) removeLocked(w *waiter) {
	for i, other := range s.line {
		if other == w {
			s.line = append(s.line[:i], s.line[i+1:]...)
			return
		}
	}
}

func (s *Slots) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

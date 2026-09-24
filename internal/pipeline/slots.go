package pipeline

import (
	"context"
	"sync"
)

// Slots limits how much converting happens at once.
//
// Packaging a film and rendering a lab clip are the same work: x265 on this
// machine's processor, which already uses every core it can find. Running two
// at once finishes both later than running them one after the other would, and
// makes a nonsense of both estimates. So they share a small number of slots,
// and whatever cannot start waits.
//
// Reading discs is not limited here. A drive can only do one thing at a time
// and that is handled where the drive is, which is the whole point: discs keep
// going in while films convert behind them.
type Slots struct {
	tokens chan struct{}

	mu      sync.Mutex
	waiting int
	running int
}

// NewSlots makes a limiter. A count below one is treated as one: nothing is
// gained by allowing none.
func NewSlots(count int) *Slots {
	if count < 1 {
		count = 1
	}

	s := &Slots{tokens: make(chan struct{}, count)}
	for i := 0; i < count; i++ {
		s.tokens <- struct{}{}
	}
	return s
}

// Take waits for a slot.
//
// Returns the context's error if the wait is abandoned, so a job stopped while
// queued stops rather than starting later out of turn.
func (s *Slots) Take(ctx context.Context) error {
	s.mu.Lock()
	s.waiting++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.waiting--
		s.mu.Unlock()
	}()

	select {
	case <-s.tokens:
		s.mu.Lock()
		s.running++
		s.mu.Unlock()
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

// Give returns a slot.
func (s *Slots) Give() {
	s.mu.Lock()
	if s.running > 0 {
		s.running--
	}
	s.mu.Unlock()

	select {
	case s.tokens <- struct{}{}:
	default:
		// More returned than taken; nothing sensible to do but carry on.
	}
}

// Busy reports how many are converting and how many are waiting their turn.
func (s *Slots) Busy() (running, waiting int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.waiting
}

// Count is how many may convert at once.
func (s *Slots) Count() int { return cap(s.tokens) }

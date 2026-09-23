package web

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/eject"
	"github.com/arfabit/arfabit/internal/store"
)

// How often the drive is asked what it holds.
//
// Asking wakes the drive, so it is asked as seldom as the situation allows:
//
//   - While a job is running the drive is not asked at all. The disc is known,
//     nothing can change, and a job takes hours — during which a poll every
//     five seconds would keep the drive spinning the whole time for nothing.
//   - An empty drive is asked often, because somebody putting a disc in wants
//     it noticed.
//   - A drive with a disc already in it is asked rarely: the only thing left
//     to notice is the disc being taken out, which nobody is waiting on.
const (
	driveWatchEmpty  = 5 * time.Second
	driveWatchLoaded = 30 * time.Second
)

// driveWatcher keeps the page told what is in the drive.
//
// Asking a person to press "look for a disc" is asking them to do the
// program's job. ARFABIT watches instead, and says what it sees.
type driveWatcher struct {
	mu     sync.Mutex
	drives []disc.Drive
}

// Drives returns what was last seen.
func (s *Server) Drives() []disc.Drive {
	s.drives.mu.Lock()
	defer s.drives.mu.Unlock()

	out := make([]disc.Drive, len(s.drives.drives))
	copy(out, s.drives.drives)
	return out
}

// WatchDrives polls until the context ends, telling the page when what is in
// the drive changes.
func (s *Server) WatchDrives(ctx context.Context) {
	check := func() {
		// A job owns the drive. Asking it anything now would wake it every few
		// seconds for hours, and could not tell us anything we do not know.
		if job := s.Runner.Current(); job != nil && job.State == store.StateRunning {
			return
		}

		// Skip a turn rather than queue behind a scan or a rip. Two
		// makemkvcon processes on one drive drop it out of LibreDrive and
		// then time out mid-read, which ruins the job in progress.
		found, ok := s.Backend.DrivesIfFree()
		if !ok {
			return
		}

		s.drives.mu.Lock()
		changed := !reflect.DeepEqual(found, s.drives.drives)
		s.drives.drives = found
		s.drives.mu.Unlock()

		if changed {
			s.events.send("drives", found)
		}
	}

	check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.nextWatchDelay()):
			check()
		}
	}
}

// nextWatchDelay is how long to leave the drive alone before asking again.
func (s *Server) nextWatchDelay() time.Duration {
	if job := s.Runner.Current(); job != nil && job.State == store.StateRunning {
		// Come back soon enough to notice the job finishing, without touching
		// the drive in the meantime.
		return driveWatchLoaded
	}

	for _, d := range s.Drives() {
		if d.Loaded {
			return driveWatchLoaded
		}
	}
	return driveWatchEmpty
}

func (s *Server) handleDrives(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Drives())
}

// handleEject opens the drive.
//
// Many slot and tray drives ignore their own button while a program holds
// them, so this is not a convenience: without it the disc can be stuck.
func (s *Server) handleEject(w http.ResponseWriter, r *http.Request) {
	if job := s.Runner.Current(); job != nil && job.State == store.StateRunning {
		writeError(w, "A disc is being worked on. Stop it first, or wait for it to finish.", nil)
		return
	}

	device, hadDisc := "", false
	for _, d := range s.Drives() {
		device, hadDisc = d.Device, d.Loaded
		break
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result := eject.Eject(ctx, device)
	writeJSON(w, map[string]any{
		"ok":      result.OK,
		"message": result.Describe(hadDisc),
	})
}

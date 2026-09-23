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

// driveWatchInterval is how often the drive is asked what it holds.
//
// Listing drives does not open the disc, so it is cheap. Five seconds is
// quick enough that putting a disc in feels noticed, without keeping the
// drive busy.
const driveWatchInterval = 5 * time.Second

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
		found, err := s.Backend.Drives()
		if err != nil {
			// A drive that cannot be asked is reported as no drive rather
			// than as an error: the page says "no disc drive found", which is
			// what the person sees anyway.
			found = nil
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

	ticker := time.NewTicker(driveWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
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

	device := ""
	for _, d := range s.Drives() {
		device = d.Device
		break
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result := eject.Eject(ctx, device)
	writeJSON(w, map[string]any{
		"ok":      result.OK,
		"message": result.Describe(),
	})
}

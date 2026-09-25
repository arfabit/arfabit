package web

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"time"

	"fmt"

	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/eject"
)

// driveWatchEmpty is how often an empty drive is asked what it holds.
//
// Asking wakes the drive, so it is only asked when the answer could change in
// a way somebody is waiting on — which means an empty drive, and nothing else:
//
//   - A drive with a disc in it is not asked at all. The only thing left to
//     notice is the disc being taken out, and nobody is standing there waiting
//     to be told that. Asking anyway spun the drive up every thirty seconds
//     for as long as a disc sat in it.
//   - A drive being used by a job is not asked either. The disc is known, it
//     cannot change, and a job takes hours.
//
// Anything that might have changed the answer pokes the watcher instead, so
// the page is still right after ejecting or finishing a disc.
const driveWatchEmpty = 5 * time.Second

// driveWatcher keeps the page told what is in the drive.
//
// Asking a person to press "look for a disc" is asking them to do the
// program's job. ARFABIT watches instead, and says what it sees.
type driveWatcher struct {
	mu     sync.Mutex
	drives []disc.Drive
	poke   chan struct{}
}

// Poke asks the watcher to look again.
//
// Called after anything that could have changed what is in the drive, which is
// how the page stays right without the drive being asked on a timer.
func (s *Server) Poke() {
	s.drives.mu.Lock()
	poke := s.drives.poke
	s.drives.mu.Unlock()

	if poke == nil {
		return
	}
	select {
	case poke <- struct{}{}:
	default:
		// One pending look is as good as two.
	}
}

// discLoaded reports whether a disc is known to be in the drive.
func (s *Server) discLoaded() bool {
	s.drives.mu.Lock()
	defer s.drives.mu.Unlock()

	for _, d := range s.drives.drives {
		if d.Loaded {
			return true
		}
	}
	return false
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
		// A job holding the drive would be disturbed by the question, and it
		// can tell us nothing we do not already know.
		if s.Runner.DriveIsBusy() != nil {
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

	s.drives.mu.Lock()
	s.drives.poke = make(chan struct{}, 1)
	poke := s.drives.poke
	s.drives.mu.Unlock()

	check()

	for {
		// A disc already in the drive, or one being read, means there is
		// nothing worth waking the drive to find out. Wait to be poked.
		var tick <-chan time.Time
		if !s.discLoaded() && s.Runner.DriveIsBusy() == nil {
			timer := time.NewTimer(driveWatchEmpty)
			tick = timer.C
			defer timer.Stop()
		}

		select {
		case <-ctx.Done():
			return
		case <-poke:
			check()
		case <-tick:
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
	// Only a job holding the drive prevents ejecting. A film being converted
	// has long since finished with the disc.
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		writeError(w, fmt.Sprintf("The drive is busy with %s. Wait for that disc to come out.", busy.Name()), nil)
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

	// The drive has certainly changed, so look rather than wait to be asked.
	s.Poke()

	writeJSON(w, map[string]any{
		"ok":      result.OK,
		"message": result.Describe(hadDisc),
	})
}

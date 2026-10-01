package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/update"
)

// Updates on the page and in the app's menu (docs/RELEASE.md §6). ARFABIT
// finds, checks and stages a new version; the app installs it when ARFABIT
// exits with 75.

// updateState is what the page is sent about updates.
type updateState struct {
	// Off says why this copy does not update itself, when it does not.
	Off string `json:"off,omitempty"`

	Status   *update.Status        `json:"status,omitempty"`
	Settings *store.UpdateSettings `json:"settings,omitempty"`
}

// SetUpdater gives the server the copy's updater, and sends the page every
// change to what it is doing.
func (s *Server) SetUpdater(u *update.Updater) {
	s.Updater = u
	u.OnChange = func(update.Status) {
		s.events.send("update", s.updateState())
		s.events.send("shell", s.shellState())
	}
}

func (s *Server) updateState() updateState {
	if s.Updater == nil {
		return updateState{Off: s.UpdatesOff}
	}
	status := s.Updater.Status()
	settings := s.Store.UpdateSettings()
	return updateState{Status: &status, Settings: &settings}
}

// updateReady is the version waiting to be installed, for the app's menu.
func (s *Server) updateReady() string {
	if s.Updater == nil {
		return ""
	}
	if ready := s.Updater.Status().Ready; ready != nil {
		return ready.Version
	}
	return ""
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.updateState())
}

// handleCheckForUpdate is Check for updates. The check goes on after the
// reply; the page hears how it went from the event stream.
func (s *Server) handleCheckForUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, s.UpdatesOff, nil)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.Updater.Check(ctx)
	}()
	writeJSON(w, s.updateState())
}

// handleUpdateSettings changes Check once a day or Install updates by
// themselves. Either may be left out to leave it as it is.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CheckDaily          *bool `json:"check_daily"`
		InstallByThemselves *bool `json:"install_by_themselves"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}

	_, err := s.Store.ChangeUpdateSettings(func(c *store.UpdateSettings) {
		if req.CheckDaily != nil {
			c.CheckDaily = *req.CheckDaily
		}
		if req.InstallByThemselves != nil {
			c.InstallByThemselves = *req.InstallByThemselves
		}
	})
	if err != nil {
		writeError(w, "ARFABIT could not save that setting.", err)
		return
	}

	state := s.updateState()
	s.events.send("update", state)
	writeJSON(w, state)
}

// handleInstallUpdate is Update and restart, on the page or in the app's
// menu. Like Stop, it waits for nothing to be running: an update never
// interrupts a disc.
func (s *Server) handleInstallUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, s.UpdatesOff, nil)
		return
	}
	if s.Updater.Status().Ready == nil {
		writeError(w, "There is no new version waiting to be installed.", nil)
		return
	}
	if len(s.Runner.Active()) > 0 {
		writeError(w, "A disc is being worked on. The update will wait until it is done, or until it is stopped.", nil)
		return
	}
	if s.Restart == nil {
		writeError(w, "This copy of ARFABIT cannot restart itself.", nil)
		return
	}

	// Asked for by the person, so a version that did not start before is
	// tried again.
	s.Updater.Retry()

	writeJSON(w, map[string]bool{"restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		if err := s.Restart(); err != nil {
			s.events.send("restart", map[string]string{"detail": err.Error()})
		}
	}()
}

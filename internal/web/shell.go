package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"

	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

// What ARFABIT's app needs from it, beyond what the page does
// (docs/RELEASE.md §1): the settings it follows, a place to say what the
// system did with Start at login, and one line saying what is happening.
//
// The app holds no knowledge of discs or jobs. Everything it shows comes from
// here, and everything it changes goes through here, as the page's does.

// LoginReport is what the app says the system did with Start at login.
//
// On macOS State is SMAppService's own status, by its Swift name: "enabled",
// "requiresApproval", "notRegistered" or "notFound". Detail is any error the
// system gave, verbatim.
type LoginReport struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
}

// shellState is what the app and the page are sent about the app.
type shellState struct {
	store.ShellSettings

	// Login is the app's latest report, absent until it has made one.
	Login *LoginReport `json:"login,omitempty"`

	// Status is the line the app shows at the top of its menu.
	Status string `json:"status"`

	// Shell says whether the app started ARFABIT. Without it, these settings
	// have nobody to follow them.
	Shell bool `json:"shell"`

	// Platform is runtime.GOOS, so the page can name the menu bar or the
	// notification area, and the right settings app.
	Platform string `json:"platform"`
}

func (s *Server) shellState() shellState {
	s.shellMu.Lock()
	login := s.login
	s.shellMu.Unlock()

	return shellState{
		ShellSettings: s.Store.ShellSettings(),
		Login:         login,
		Status:        statusLine(s.Runner.Active()),
		Shell:         s.Shell,
		Platform:      runtime.GOOS,
	}
}

// handleShell reports the app's settings, its latest report and the status
// line, for the app when it connects and for the page.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.shellState())
}

// handleShellSettings changes Show the icon or Start at login, from the page
// or from the app's own menu. Either may be left out to leave it as it is.
func (s *Server) handleShellSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShowIcon     *bool `json:"show_icon"`
		StartAtLogin *bool `json:"start_at_login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}

	_, err := s.Store.ChangeShellSettings(func(c *store.ShellSettings) {
		if req.ShowIcon != nil {
			c.ShowIcon = *req.ShowIcon
		}
		if req.StartAtLogin != nil {
			c.StartAtLogin = *req.StartAtLogin
		}
	})
	if err != nil {
		writeError(w, "ARFABIT could not save that setting.", err)
		return
	}

	state := s.shellState()
	s.events.send("shell", state)
	writeJSON(w, state)
}

// handleLoginReport records what the system did with Start at login, as the
// app found it. The page shows this rather than the setting alone, so it never
// disagrees with the system.
func (s *Server) handleLoginReport(w http.ResponseWriter, r *http.Request) {
	var report LoginReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		writeError(w, "ARFABIT could not read that report.", err)
		return
	}

	s.shellMu.Lock()
	s.login = &report
	s.shellMu.Unlock()

	state := s.shellState()
	s.events.send("shell", state)
	writeJSON(w, state)
}

// sendStatus tells the app what is happening, when that has changed.
func (s *Server) sendStatus() {
	line := statusLine(s.Runner.Active())

	s.shellMu.Lock()
	changed := line != s.status
	s.status = line
	s.shellMu.Unlock()

	if changed {
		s.events.send("status", map[string]string{"line": line})
	}
}

// statusLine says in a few words what ARFABIT is doing: "Nothing running",
// "Copying Blade Runner — 42%", and when there is more than one thing, how
// many more.
func statusLine(active []*pipeline.Job) string {
	if len(active) == 0 {
		return "Nothing running"
	}

	// The first thing being worked on, rather than one waiting its turn.
	first := active[0]
	for _, job := range active {
		if job.Stage != store.StageQueued {
			first = job
			break
		}
	}

	line := fmt.Sprintf("%s %s", doing(first.Stage), first.Name())
	if p := first.Progress.Percent; p > 0 && p < 100 {
		line += fmt.Sprintf(" — %d%%", int(p))
	}
	if more := len(active) - 1; more > 0 {
		line += fmt.Sprintf(", and %d more", more)
	}
	return line
}

// doing is what a stage is called in a sentence about a disc or a film.
func doing(stage store.Stage) string {
	switch stage {
	case store.StageScan, store.StagePlan:
		return "Reading"
	case store.StageRip:
		return "Copying"
	case store.StageEject:
		return "Ejecting"
	case store.StageQueued:
		return "Waiting to make"
	case store.StagePackage:
		return "Making"
	case store.StageLab:
		return "Comparing parts of"
	case store.StageOCR:
		return "Reading the subtitles of"
	case store.StageDeliver:
		return "Filing"
	}
	return "Working on"
}

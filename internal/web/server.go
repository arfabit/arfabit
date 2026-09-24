// Package web serves ARFABIT's interface.
//
// The page is server-rendered HTML with a small amount of JavaScript and a
// server-sent event stream for live updates. There is no build step and no
// package manager: the whole interface is three embedded files.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/arfabit/arfabit/internal/autostart"
	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/doctor"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

//go:embed static/*
var assets embed.FS

//go:embed templates/*.html
var templates embed.FS

// Server is the web interface.
type Server struct {
	Config  config.Config
	Store   *store.Store
	Runner  *pipeline.Runner
	Backend *makemkv.Backend

	// Restart starts ARFABIT again. Set by the program that owns the process,
	// because only it knows how to shut down tidily first.
	Restart func() error

	// Quit stops ARFABIT. The reason is shown in the terminal, so a copy
	// standing aside for a newer one does not read as a mystery shutdown.
	Quit func(reason string)

	tmpl    *template.Template
	events  *eventStream
	drives  driveWatcher
	started time.Time

	// building guards the film list download, and lab guards the clip
	// renderer. A button can be clicked twice; the server is where "once" has
	// to be true.
	building atomic.Bool
	lab      atomic.Bool
}

// New prepares the server.
func New(cfg config.Config, st *store.Store, runner *pipeline.Runner, backend *makemkv.Backend) (*Server, error) {
	tmpl, err := template.ParseFS(templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		Config:  cfg,
		Store:   st,
		Runner:  runner,
		Backend: backend,
		tmpl:    tmpl,
		events:  newEventStream(),
		started: time.Now(),
	}

	// Every job change is pushed to open pages, so nothing polls and nothing
	// reloads underneath the reader (§14).
	runner.OnUpdate = func(job *pipeline.Job) {
		s.events.send("job", job)
	}

	// Log lines go out as they are written, so a stage that takes minutes
	// shows its working instead of sitting silent.
	runner.OnLog = func(e pipeline.Entry) {
		s.events.send("log", e)
	}

	return s, nil
}

// Close ends every open page connection.
//
// Called before the HTTP server is shut down, because those connections stay
// open for as long as a page is on screen and would otherwise hold shutdown up
// indefinitely.
func (s *Server) Close() {
	s.events.closeAll()
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.Handle("GET /static/", noCache(http.FileServer(http.FS(assets))))

	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/drives", s.handleDrives)
	mux.HandleFunc("GET /api/drive-health", s.handleDriveHealth)
	mux.HandleFunc("POST /api/drive-free", s.handleFreeDrive)
	mux.HandleFunc("GET /api/masters", s.handleMasters)
	mux.HandleFunc("GET /api/lab", s.handleLabClips)
	mux.HandleFunc("POST /api/lab", s.handleLab)
	mux.HandleFunc("POST /api/eject", s.handleEject)
	mux.HandleFunc("GET /api/doctor", s.handleDoctor)
	mux.HandleFunc("GET /api/log", s.handleLog)

	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/plan", s.handleUpdatePlan)

	mux.HandleFunc("POST /api/title", s.handleChooseTitle)
	mux.HandleFunc("GET /api/index", s.handleIndexStatus)
	mux.HandleFunc("POST /api/index", s.handleBuildIndex)

	mux.HandleFunc("POST /api/restart", s.handleRestart)
	mux.HandleFunc("POST /api/quit", s.handleQuit)

	mux.HandleFunc("GET /api/autostart", s.handleAutostart)
	mux.HandleFunc("POST /api/autostart", s.handleSetAutostart)

	return mux
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"NodeName": s.Config.Node.Name,
		"Started":  s.started.Format("3:04 PM"),
		"Library":  s.Config.Paths.Library,
		"Masters":  s.Config.Paths.Masters,
		"Profile":  s.Config.Profile,
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// state is everything a page needs to render itself.
type state struct {
	// Job is whatever is asking for a decision: a Plan waiting to be started.
	Job *pipeline.Job `json:"job"`

	// Active is everything being worked on. More than one is ordinary: a disc
	// being converted does not need the drive, so the next one can go in.
	Active []*pipeline.Job `json:"active"`

	// DriveBusy names the job holding the drive, if any.
	DriveBusy string `json:"drive_busy,omitempty"`

	// ConversionsAtOnce is how many films may convert simultaneously, and
	// Queued how many are waiting for a turn.
	ConversionsAtOnce int `json:"conversions_at_once"`
	Queued            int `json:"queued"`

	Recent   []*store.Job `json:"recent"`
	Drives   []disc.Drive `json:"drives"`
	NodeName string       `json:"node_name"`
	Paths    config.Paths `json:"paths"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	recent, _ := s.Store.AllJobs()
	if len(recent) > 20 {
		recent = recent[:20]
	}

	current := s.Runner.Current()
	if current != nil && current.State != store.StateWaiting {
		// Only a Plan awaiting an answer belongs in the decision slot.
		current = nil
	}

	reply := state{
		Job:      current,
		Active:   s.Runner.Active(),
		Recent:   recent,
		Drives:   s.Drives(),
		NodeName: s.Config.Node.Name,
		Paths:    s.Config.Paths,
	}
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		reply.DriveBusy = busy.Title
	}
	if s.Runner.Slots != nil {
		reply.ConversionsAtOnce = s.Runner.Slots.Count()
		reply.Queued = s.Runner.Queued()
	}

	writeJSON(w, reply)
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	writeJSON(w, doctor.Run(ctx, s.Config))
}

// handleLog returns a job's log lines.
//
// Lines carry stable ids and are only ever appended, so the page can add new
// ones without redrawing — which is what keeps a search or a text selection
// from being thrown away (§14).
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	job := s.Runner.Current()
	if job == nil || job.Log == nil {
		writeJSON(w, []pipeline.Entry{})
		return
	}
	writeJSON(w, job.Log.Entries())
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var loaded *disc.Drive
	for _, d := range s.Drives() {
		if d.Loaded {
			found := d
			loaded = &found
			break
		}
	}
	if loaded == nil {
		writeError(w, "There is no disc in the drive.", nil)
		return
	}

	go func() {
		// The scan runs on its own so the page stays responsive; progress
		// arrives over the event stream.
		_, _ = s.Runner.Scan(context.Background(), *loaded)
	}()

	writeJSON(w, map[string]string{"started": loaded.Name})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := s.Runner.Start(context.Background()); err != nil {
		writeError(w, err.Error(), nil)
		return
	}
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// With several discs in flight, "stop" has to say which.
	if req.ID == "" {
		s.Runner.Stop()
		writeJSON(w, map[string]bool{"stopped": true})
		return
	}

	if err := s.Runner.StopJob(req.ID); err != nil {
		writeError(w, "That disc is no longer being worked on.", err)
		return
	}
	writeJSON(w, map[string]bool{"stopped": true})
}

// handleUpdatePlan applies the user's changes to the Plan before it runs.
func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	job := s.Runner.Current()
	if job == nil || job.Plan == nil {
		writeError(w, "There is no disc waiting.", nil)
		return
	}

	var change struct {
		Audio     map[int]bool `json:"audio"`
		Subtitles map[int]bool `json:"subtitles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}

	for i := range job.Plan.Audio {
		if selected, ok := change.Audio[job.Plan.Audio[i].SourceIndex]; ok {
			job.Plan.Audio[i].Selected = selected
		}
	}
	for i := range job.Plan.Subtitles {
		if selected, ok := change.Subtitles[job.Plan.Subtitles[i].SourceIndex]; ok {
			job.Plan.Subtitles[i].Selected = selected
		}
	}

	_ = s.Store.SaveJob(job.Job)
	writeJSON(w, job.Plan)
}

// handleChooseTitle applies the name and year the user picked.
func (s *Server) handleChooseTitle(w http.ResponseWriter, r *http.Request) {
	job := s.Runner.Current()
	if job == nil {
		writeError(w, "There is no disc waiting.", nil)
		return
	}

	var choice struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&choice); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}
	if strings.TrimSpace(choice.Title) == "" {
		writeError(w, "A movie needs a name.", nil)
		return
	}

	job.Title = strings.TrimSpace(choice.Title)
	job.Year = choice.Year
	_ = s.Store.SaveJob(job.Job)

	writeJSON(w, job.Job)
}

// handleBuildIndex downloads the film list.
//
// It runs in the background and reports over the event stream, because it is a
// 200 MB download and the page must stay usable throughout.
func (s *Server) handleBuildIndex(w http.ResponseWriter, r *http.Request) {
	if !s.building.CompareAndSwap(false, true) {
		writeError(w, "The film list is already downloading.", nil)
		return
	}

	go func() {
		defer s.building.Store(false)

		path := meta.IndexPath(s.Config.Paths.Data)
		s.events.send("index", map[string]any{"state": "downloading", "read": 0, "path": path})

		// Progress is reported as it arrives, but not on every read: the
		// callback fires thousands of times a second and the page only needs
		// to see movement.
		var lastSent int64
		onProgress := func(read int64) {
			const step = 2 << 20
			if read-lastSent < step {
				return
			}
			lastSent = read
			s.events.send("index", map[string]any{"state": "downloading", "read": read, "path": path})
		}

		ix, err := meta.BuildIndex(s.Config.Paths.Data, false, onProgress)
		if err != nil {
			s.events.send("index", map[string]any{
				"state":  "stopped",
				"detail": err.Error(),
				"path":   path,
			})
			return
		}

		s.Runner.Index = ix
		s.events.send("index", map[string]any{
			"state": "ready",
			"count": len(ix.Entries),
			"path":  path,
			"built": ix.Built,
		})
	}()

	writeJSON(w, map[string]string{"state": "downloading"})
}

// handleRestart starts ARFABIT again.
//
// A disc being worked on stops this: a restart mid-rip would leave the job
// unfinished, and the person clicking is unlikely to mean that.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.Restart == nil {
		writeError(w, "This copy of ARFABIT cannot restart itself.", nil)
		return
	}

	if job := s.Runner.Current(); job != nil && job.State == store.StateRunning {
		writeError(w, "A disc is being worked on. Stop it first, or wait for it to finish.", nil)
		return
	}

	// The reply goes out before the restart, so the page knows to start
	// waiting rather than watching the connection die.
	writeJSON(w, map[string]bool{"restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		// A moment for the reply to reach the browser.
		time.Sleep(250 * time.Millisecond)
		if err := s.Restart(); err != nil {
			s.events.send("restart", map[string]string{"detail": err.Error()})
		}
	}()
}

// handleQuit stops ARFABIT.
//
// Somebody who started it from a terminal can press Ctrl+C, but somebody whose
// computer starts it, or who closed that window, has no way to stop it at all.
// So the page offers one.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if s.Quit == nil {
		writeError(w, "This copy of ARFABIT cannot stop itself.", nil)
		return
	}

	if len(s.Runner.Active()) > 0 {
		writeError(w, "A disc is being worked on. Stop it first, or wait for it to finish.", nil)
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	reason := "asked from the page"
	if req.Reason != "" {
		reason = req.Reason
	}

	writeJSON(w, map[string]bool{"stopping": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(250 * time.Millisecond)
		s.Quit(reason)
	}()
}

// handleIndexStatus says whether the film list is present and where it lives.
//
// "Where" matters: a download that names no destination is indistinguishable
// from one that is not happening.
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"state": "missing",
		"path":  meta.IndexPath(s.Config.Paths.Data),
	}

	if ix := s.Runner.Index; ix != nil {
		status["state"] = "ready"
		status["count"] = len(ix.Entries)
		status["built"] = ix.Built
	}
	writeJSON(w, status)
}

// handleAutostart reports whether ARFABIT starts with the computer.
func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, autostart.Current())
}

// handleSetAutostart turns the startup entry on or off.
func (s *Server) handleSetAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}

	var (
		status autostart.Status
		err    error
	)
	if req.Enabled {
		status, err = autostart.Enable()
	} else {
		status, err = autostart.Disable()
	}
	if err != nil {
		writeError(w, "ARFABIT could not change the startup setting.", err)
		return
	}

	writeJSON(w, status)
}

// noCache stops the browser holding on to the page's own files.
//
// They are built into the program, so a new copy of ARFABIT means new files —
// and a browser serving yesterday's script against today's server produces
// failures that make no sense to anybody.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeError sends a plain-language message with the raw detail attached.
//
// The detail is never a summary: the page shows it behind "Technical details"
// so the real account is always one click away (§15).
func writeError(w http.ResponseWriter, message string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)

	payload := map[string]string{"message": message}
	if err != nil {
		payload["detail"] = fmt.Sprint(err)
	}
	_ = json.NewEncoder(w).Encode(payload)
}

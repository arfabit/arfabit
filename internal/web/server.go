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

	// Quit stops ARFABIT.
	Quit func()

	tmpl   *template.Template
	events *eventStream
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
	}

	// Every job change is pushed to open pages, so nothing polls and nothing
	// reloads underneath the reader (§14).
	runner.OnUpdate = func(job *pipeline.Job) {
		s.events.send("job", job)
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
	mux.Handle("GET /static/", http.FileServer(http.FS(assets)))

	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/doctor", s.handleDoctor)
	mux.HandleFunc("GET /api/log", s.handleLog)

	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/plan", s.handleUpdatePlan)

	mux.HandleFunc("POST /api/title", s.handleChooseTitle)
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
	Job      *pipeline.Job `json:"job"`
	Recent   []*store.Job  `json:"recent"`
	Drives   []disc.Drive  `json:"drives"`
	NodeName string        `json:"node_name"`
	Paths    config.Paths  `json:"paths"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	recent, _ := s.Store.AllJobs()
	if len(recent) > 20 {
		recent = recent[:20]
	}

	writeJSON(w, state{
		Job:      s.Runner.Current(),
		Recent:   recent,
		NodeName: s.Config.Node.Name,
		Paths:    s.Config.Paths,
	})
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
	drives, err := s.Backend.Drives()
	if err != nil {
		writeError(w, "ARFABIT could not ask about your disc drive.", err)
		return
	}

	var loaded *disc.Drive
	for i := range drives {
		if drives[i].Loaded {
			loaded = &drives[i]
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
	s.Runner.Stop()
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
	go func() {
		s.events.send("index", map[string]any{"state": "downloading", "read": 0})

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
			s.events.send("index", map[string]any{"state": "downloading", "read": read})
		}

		ix, err := meta.BuildIndex(s.Config.Paths.Data, false, onProgress)
		if err != nil {
			s.events.send("index", map[string]any{
				"state":  "stopped",
				"detail": err.Error(),
			})
			return
		}

		s.Runner.Index = ix
		s.events.send("index", map[string]any{
			"state": "ready",
			"count": len(ix.Entries),
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

	if job := s.Runner.Current(); job != nil && job.State == store.StateRunning {
		writeError(w, "A disc is being worked on. Stop it first, or wait for it to finish.", nil)
		return
	}

	writeJSON(w, map[string]bool{"stopping": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(250 * time.Millisecond)
		s.Quit()
	}()
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

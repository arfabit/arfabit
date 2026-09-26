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
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/arfabit/arfabit/internal/autostart"
	"github.com/arfabit/arfabit/internal/blueprints"
	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/doctor"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/ocr"
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

	// Blueprints are the named settings people make and edit here.
	Blueprints *blueprints.Store

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

	// hashes remembers the SHA-256 of the subtitle files the page asks
	// about, until they change.
	hashes pipeline.Hashes

	// building guards the film list download. A button can be clicked twice;
	// the server is where "once" has to be true. Projects need no such guard:
	// they are jobs, and the queue decides when they run.
	building atomic.Bool
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

		// A disc that has been ejected or a job that has ended changes what
		// is in the drive, and is the moment to look rather than a timer.
		switch job.Stage {
		case store.StageEject, store.StageDeliver:
			s.Poke()
		}
		if job.State != store.StateRunning {
			s.Poke()
		}
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
	mux.HandleFunc("POST /api/drive-setting", s.handleDriveSetting)
	mux.HandleFunc("GET /api/originals", s.handleOriginals)
	mux.HandleFunc("GET /api/blueprints", s.handleBlueprints)
	mux.HandleFunc("POST /api/blueprints", s.handleSaveBlueprint)
	mux.HandleFunc("DELETE /api/blueprints/{name}", s.handleDeleteBlueprint)
	mux.HandleFunc("POST /api/blueprints/default", s.handleDefaultBlueprint)
	mux.HandleFunc("GET /api/original", s.handleOriginal)
	mux.HandleFunc("POST /api/project/fill", s.handleFillProject)
	mux.HandleFunc("POST /api/project", s.handleStartProject)
	mux.HandleFunc("POST /api/resume", s.handleResume)
	mux.HandleFunc("POST /api/eject", s.handleEject)
	mux.HandleFunc("GET /api/doctor", s.handleDoctor)
	mux.HandleFunc("GET /api/log", s.handleLog)
	mux.HandleFunc("GET /api/jobs/{id}/low-confidence/{n}/picture", s.handleLowConfidencePicture)
	mux.HandleFunc("POST /api/jobs/{id}/low-confidence/{n}", s.handleChooseLowConfidence)
	mux.HandleFunc("POST /api/jobs/{id}/fine", s.handleSubtitlesAreFine)
	mux.HandleFunc("POST /api/jobs/{id}/bring-up-to-date", s.handleBringUpToDate)

	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("POST /api/line", s.handleMoveInLine)
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
		"Clips":    s.Config.Paths.Clips,
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// state is everything a page needs to render itself.
type state struct {
	// Job is the disc whose Plan can be changed: waiting to be started, or
	// started with some of it still to begin. Editing says what can.
	Job     *pipeline.Job     `json:"job"`
	Editing *pipeline.Editing `json:"editing,omitempty"`

	// Active is everything being worked on. More than one is ordinary: a disc
	// being converted does not need the drive, so the next one can go in.
	Active []*pipeline.Job `json:"active"`

	// Existing lists files the waiting Plan would replace, which it will not
	// do. The Plan cannot start until they are moved or the edition changes.
	Existing []string `json:"existing,omitempty"`

	// DriveBusy names the job holding the drive, if any.
	DriveBusy string `json:"drive_busy,omitempty"`

	// ConversionsAtOnce is how many films may convert simultaneously, and
	// Queued how many are waiting for a turn.
	ConversionsAtOnce int `json:"conversions_at_once"`
	Queued            int `json:"queued"`

	// Line is the jobs waiting for the processor, by id, in the order they
	// will start.
	Line []string `json:"line"`

	// Resumable names the stopped jobs that can simply be started again,
	// which is anything working from a copy that is still there.
	Resumable map[string]bool `json:"resumable,omitempty"`

	// Now is ARFABIT's own time, so a page on another computer can keep its
	// clocks in step with the times it is sent.
	Now time.Time `json:"now"`

	Recent []*store.Job `json:"recent"`

	// DriveSettings says what each drive does when a disc goes in, by its
	// name.
	DriveSettings map[string]store.DriveSetting `json:"drive_settings"`

	// Copies says how each film's SRTs stand against those beside its
	// original, by the task that made them; CopiesOf says the same by the
	// SRT beside the original, for the OCR task that read it (§10).
	Copies   map[string][]copyState `json:"copies,omitempty"`
	CopiesOf map[string][]copyState `json:"copies_of,omitempty"`

	Drives   []disc.Drive `json:"drives"`
	NodeName string       `json:"node_name"`
	Paths    config.Paths `json:"paths"`

	// OCR says whether this computer can read picture subtitles into text
	// (§10), and OCRNote says so plainly when it cannot.
	OCR     bool   `json:"ocr"`
	OCRNote string `json:"ocr_note,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	// Recent tasks are the ones that are over. Anything working or waiting
	// is in the queue, and a disc waiting to be started is its Plan. Subtitles
	// still to be checked stay in the list however long ago they were read,
	// so a fix can always be found (§10).
	all, _ := s.Store.AllJobs()
	recent := []*store.Job{}
	for _, job := range all {
		if job.State != store.StateDone && job.State != store.StateStopped {
			continue
		}
		if len(recent) < 20 || job.ToCheck() {
			recent = append(recent, job)
		}
	}

	byTask, byFrom := s.copiesOf(all)
	copies, copiesOf := map[string][]copyState{}, map[string][]copyState{}
	for _, job := range recent {
		if c := byTask[job.ID]; len(c) > 0 {
			copies[job.ID] = c
		}
		if job.Reading != nil {
			if c := byFrom[filepath.Clean(job.Reading.SRT)]; len(c) > 0 {
				copiesOf[job.ID] = c
			}
		}
	}

	resumable := map[string]bool{}
	for _, job := range recent {
		if job.State == store.StateStopped && pipeline.Resumable(job) {
			resumable[job.ID] = true
		}
	}

	// The Plan shown is a disc's that can still be changed: waiting to be
	// started, or started with some of it yet to begin.
	current := s.Runner.Editable()

	reply := state{
		Now:       time.Now(),
		Resumable: resumable,
		Job:       current,
		Active:    s.Runner.Active(),
		Recent:    recent,

		DriveSettings: s.Store.DriveSettings(),
		Copies:        copies,
		CopiesOf:      copiesOf,
		Drives:        s.Drives(),
		NodeName:      s.Config.Node.Name,
		Paths:         s.Config.Paths,
		OCR:           s.Runner.OCR != nil,
	}
	if !reply.OCR {
		reply.OCRNote = ocr.NotAvailable
	}
	if current != nil {
		editing := s.Runner.EditingOf(current)
		reply.Editing = &editing
		if !editing.Started {
			reply.Existing = s.Runner.Existing(current)
		}
	}
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		reply.DriveBusy = busy.Name()
	}
	if s.Runner.Slots != nil {
		reply.ConversionsAtOnce = s.Runner.Slots.Count()
		reply.Queued = s.Runner.Queued()
		reply.Line = s.Runner.Slots.Line()
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
	// Every disc being worked on, merged and in time order. Showing one job's
	// log while three are running would hide two of them.
	jobs := s.Runner.Active()
	if pending := s.Runner.Current(); pending != nil {
		jobs = append(jobs, pending)
	}

	seen := map[string]bool{}
	entries := []pipeline.Entry{}

	for _, job := range jobs {
		if job == nil || job.Log == nil || seen[job.ID] {
			continue
		}
		seen[job.ID] = true
		entries = append(entries, job.Log.Entries()...)
	}

	sort.SliceStable(entries, func(a, b int) bool {
		return entries[a].Time.Before(entries[b].Time)
	})

	writeJSON(w, entries)
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

// handleMoveInLine puts a waiting job at a new place in the line for the
// processor, counting from zero at the front.
func (s *Server) handleMoveInLine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
		To int    `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "That request could not be read.", err)
		return
	}

	if s.Runner.Slots == nil || !s.Runner.Slots.Move(req.ID, req.To) {
		writeError(w, "That has already started, so it cannot be moved.", nil)
		return
	}

	s.events.send("job", map[string]string{"id": req.ID})
	writeJSON(w, map[string][]string{"line": s.Runner.Slots.Line()})
}

// handleUpdatePlan applies the user's changes to a disc's Plan: before it
// starts, or while each step of it has still to start (§2).
func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	job := s.Runner.Editable()
	if job == nil || job.Plan == nil {
		writeError(w, "There is no disc waiting.", nil)
		return
	}

	var change struct {
		Read    map[int]bool   `json:"read"`
		Convert *bool          `json:"convert"`
		Edition *string        `json:"edition"`
		Project *store.Project `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
		writeError(w, "ARFABIT could not read that change.", err)
		return
	}

	var err error
	if change.Project != nil {
		err = s.Runner.UpdateProject(*change.Project)
	}
	if err == nil && change.Edition != nil {
		err = s.Runner.SetEdition(*change.Edition)
	}
	if err == nil && change.Convert != nil {
		err = s.Runner.SetConvert(context.Background(), *change.Convert)
	}
	if err == nil && change.Read != nil {
		err = s.Runner.SetRead(change.Read)
	}
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}
	writeJSON(w, job.Plan)
}

// handleChooseTitle applies the name and year the user picked.
func (s *Server) handleChooseTitle(w http.ResponseWriter, r *http.Request) {
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

	job, err := s.Runner.SetTitle(strings.TrimSpace(choice.Title), choice.Year)
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}
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

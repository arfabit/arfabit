package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	cfg := config.Defaults()
	cfg.Paths.Data = t.TempDir()
	cfg.Node.Name = "test-node"

	st, err := store.New(cfg.Paths.Data, "test-node")
	if err != nil {
		t.Fatal(err)
	}

	backend := &makemkv.Backend{}
	runner := &pipeline.Runner{
		Config:      cfg,
		Store:       st,
		Backend:     backend,
		Calibration: pipeline.NewCalibration(),
	}

	s, err := New(cfg, st, runner, backend)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHomePageRenders(t *testing.T) {
	rec := get(t, newTestServer(t), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{"ARFABIT", "test-node", "/static/app.js", "/static/app.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestStaticAssetsAreEmbedded(t *testing.T) {
	for _, path := range []string{"/static/app.css", "/static/app.js"} {
		rec := get(t, newTestServer(t), path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d", path, rec.Code)
		}
		if rec.Body.Len() < 100 {
			t.Errorf("%s served only %d bytes", path, rec.Body.Len())
		}
	}
}

func TestStateIsJSON(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/state")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got state
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("state is not valid JSON: %v", err)
	}
	if got.NodeName != "test-node" {
		t.Errorf("NodeName = %q", got.NodeName)
	}
	// Nothing has run yet, so there is no job and that is not an error.
	if got.Job != nil {
		t.Errorf("Job = %+v, want nil", got.Job)
	}
}

func TestLogIsEmptyBeforeAnyJob(t *testing.T) {
	rec := get(t, newTestServer(t), "/api/log")

	var entries []pipeline.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("log is not valid JSON: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries before any job ran", len(entries))
	}
}

// Starting with no disc waiting must say so in plain language rather than
// producing a server error.
func TestStartWithNoDiscIsExplained(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/start", nil))

	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("status = %d, want a handled response", rec.Code)
	}

	var problem map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem["message"] == "" {
		t.Error("no message was given")
	}
	for _, word := range []string{"error", "failed", "nil", "panic"} {
		if strings.Contains(strings.ToLower(problem["message"]), word) {
			t.Errorf("message reads as a fault: %q", problem["message"])
		}
	}
}

func TestEventStreamSendsToWatchers(t *testing.T) {
	es := newEventStream()
	id, ch := es.add()
	defer es.remove(id)

	es.send("job", map[string]string{"id": "one"})

	select {
	case msg := <-ch:
		if !strings.HasPrefix(msg, "event: job\n") {
			t.Errorf("message = %q", msg)
		}
		if !strings.Contains(msg, `"id":"one"`) {
			t.Errorf("payload missing: %q", msg)
		}
	default:
		t.Fatal("nothing was delivered to the watcher")
	}
}

// A browser tab that has stopped reading must never hold up a rip.
func TestEventStreamSkipsFullWatchers(t *testing.T) {
	es := newEventStream()
	id, _ := es.add()
	defer es.remove(id)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			es.send("job", map[string]int{"n": i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-make(chan struct{}):
		t.Fatal("send blocked on a watcher that was not reading")
	}
}

// Pages hold their update connection open for as long as they are on screen,
// so shutdown has to close them. Waiting for them to end on their own waits
// forever, which is what made Ctrl+C hang.
func TestCloseEndsOpenStreams(t *testing.T) {
	s := newTestServer(t)

	_, first := s.events.add()
	_, second := s.events.add()

	s.Close()

	for i, ch := range []chan string{first, second} {
		select {
		case _, open := <-ch:
			if open {
				t.Errorf("watcher %d received a message instead of being closed", i)
			}
		default:
			t.Errorf("watcher %d is still open after Close", i)
		}
	}
}

// Closing twice must not panic: shutdown may race with a page disconnecting.
func TestCloseIsSafeTwice(t *testing.T) {
	s := newTestServer(t)
	s.events.add()

	s.Close()
	s.Close()
}

// A restart part-way through a rip would leave the job unfinished, which is
// unlikely to be what the person clicking meant.
func TestRestartRefusedWhileWorking(t *testing.T) {
	s := newTestServer(t)

	var called bool
	s.Restart = func() error { called = true; return nil }
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "busy", State: store.StateRunning, Stage: store.StageRip},
	})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restart", nil))

	if called {
		t.Error("ARFABIT restarted while a disc was being worked on")
	}

	var problem map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem["message"], "Stop it first") {
		t.Errorf("the message does not say what to do: %q", problem["message"])
	}
}

// With nothing running, the reply goes out before the restart happens, so the
// page knows to start waiting rather than watching the connection die.
func TestRestartRepliesBeforeRestarting(t *testing.T) {
	s := newTestServer(t)

	done := make(chan struct{})
	s.Restart = func() error { close(done); return nil }

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/restart", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "restarting") {
		t.Errorf("body = %q", rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("the restart never ran")
	}
}

// Somebody whose computer starts ARFABIT has no terminal to press Ctrl+C in,
// so the page has to be able to stop it.
func TestQuitStopsArfabit(t *testing.T) {
	s := newTestServer(t)

	stopped := make(chan struct{})
	s.Quit = func(string) { close(stopped) }

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/quit", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Error("ARFABIT did not stop")
	}
}

// Stopping mid-rip would abandon a job, same as restarting.
func TestQuitRefusedWhileWorking(t *testing.T) {
	s := newTestServer(t)

	var called bool
	s.Quit = func(string) { called = true }
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "busy", State: store.StateRunning, Stage: store.StageRip},
	})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/quit", nil))

	if called {
		t.Error("ARFABIT stopped while a disc was being worked on")
	}
}

// The event stream is how the page learns anything at all, so it is worth
// proving end to end rather than only through the watcher list.
func TestEventStreamDeliversOverHTTP(t *testing.T) {
	s := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	finished := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(finished)
	}()

	// Wait for the handler to register before sending anything.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.events.mu.Lock()
		watching := len(s.events.watchers)
		s.events.mu.Unlock()
		if watching > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream never opened")
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.events.send("log", pipeline.Entry{ID: 1, Stage: store.StageScan, Text: "Reading the disc."})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-finished

	body := rec.Body.String()
	if !strings.Contains(body, "event: log") {
		t.Errorf("no log event reached the page:\n%s", body)
	}
	if !strings.Contains(body, "Reading the disc.") {
		t.Errorf("the message did not arrive:\n%s", body)
	}
}

// A log line written during a job must reach the page as it happens, not when
// the stage ends: a Blu-ray scan takes minutes and silence looks like failure.
func TestLogLinesReachTheStreamImmediately(t *testing.T) {
	s := newTestServer(t)

	_, ch := s.events.add()

	s.Runner.OnLog(pipeline.Entry{ID: 7, Stage: store.StageScan, Text: "Waking the drive."})

	select {
	case msg := <-ch:
		if !strings.Contains(msg, "event: log") || !strings.Contains(msg, "Waking the drive.") {
			t.Errorf("unexpected message: %q", msg)
		}
	default:
		t.Fatal("the log line never reached the stream")
	}
}

// A button can be clicked twice, so "once" has to be true on the server.
func TestIndexDownloadsOnceAtATime(t *testing.T) {
	s := newTestServer(t)
	s.building.Store(true)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/index", nil))

	var problem map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem["message"], "already downloading") {
		t.Errorf("a second download was accepted: %q", problem["message"])
	}
}

// The page should know what is in the drive without being asked to look.
func TestDrivesAreReportedInState(t *testing.T) {
	s := newTestServer(t)
	s.drives.drives = []disc.Drive{
		{Index: 0, Name: "BD-RE", Device: "/dev/rdisk8", Label: "CRIME_101", Loaded: true},
	}

	rec := get(t, s, "/api/state")

	var got state
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Drives) != 1 || !got.Drives[0].Loaded {
		t.Fatalf("drives = %+v", got.Drives)
	}
	if got.Drives[0].Label != "CRIME_101" {
		t.Errorf("label = %q", got.Drives[0].Label)
	}
}

// Ejecting mid-rip would pull a disc out from under the reader.
func TestEjectRefusedWhileWorking(t *testing.T) {
	s := newTestServer(t)
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "busy", State: store.StateRunning, Stage: store.StageRip},
	})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/eject", nil))

	if rec.Code == http.StatusOK {
		t.Error("the disc was ejected while it was being read")
	}
}

// The page's files are built into the program, so a new copy means new files.
// A browser serving yesterday's script against today's server produces
// failures that make no sense to anybody.
func TestStaticFilesAreNotCached(t *testing.T) {
	for _, path := range []string{"/static/app.js", "/static/app.css"} {
		rec := get(t, newTestServer(t), path)
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
			t.Errorf("%s may be cached: Cache-Control = %q", path, got)
		}
	}
}

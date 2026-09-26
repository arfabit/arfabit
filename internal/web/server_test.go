package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/blueprints"
	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	// Every path points somewhere temporary. A test that wrote to the real
	// originals folder would be touching somebody's films.
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Paths.Data = filepath.Join(root, "data")
	cfg.Paths.Library = filepath.Join(root, "library")
	cfg.Paths.Clips = filepath.Join(root, "clips")
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

	store, err := blueprints.Open(cfg.Paths.Data)
	if err != nil {
		t.Fatal(err)
	}
	s.Blueprints = store

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

// Asking the drive anything wakes it, so it is only asked when the answer
// could change in a way somebody is waiting on.
func TestDriveIsLeftAloneWhenThereIsNothingToLearn(t *testing.T) {
	s := newTestServer(t)

	// A disc already in it: the only thing left to notice is it being taken
	// out, and nobody is waiting to be told that.
	s.drives.drives = []disc.Drive{{Index: 0, Name: "BD-RE", Loaded: true}}
	if !s.discLoaded() {
		t.Error("a loaded drive was not recognised as loaded")
	}

	// Empty: somebody putting a disc in does want it noticed.
	s.drives.drives = []disc.Drive{{Index: 0, Name: "BD-RE", Loaded: false}}
	if s.discLoaded() {
		t.Error("an empty drive was reported as loaded")
	}
}

// Anything that could have changed what is in the drive pokes the watcher, so
// the page stays right without the drive being asked on a timer.
func TestPokeIsSafeAndDoesNotBlock(t *testing.T) {
	s := newTestServer(t)

	// Before the watcher is running there is nothing to poke, and that must
	// not be a problem.
	s.Poke()

	s.drives.poke = make(chan struct{}, 1)
	for i := 0; i < 5; i++ {
		s.Poke()
	}

	if len(s.drives.poke) != 1 {
		t.Errorf("%d pokes are queued; one pending look is as good as two", len(s.drives.poke))
	}
}

// A film being converted does not need the drive, so the page must offer to
// read the next disc rather than reporting everything as busy.
func TestStateSeparatesTheDriveFromTheWork(t *testing.T) {
	s := newTestServer(t)
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "converting", Title: "Crime 101", State: store.StateRunning, Stage: store.StagePackage},
	})

	rec := get(t, s, "/api/state")

	var got state
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Active) != 1 {
		t.Fatalf("got %d active jobs, want 1", len(got.Active))
	}
	if got.DriveBusy != "" {
		t.Errorf("the drive is reported busy with %q while only converting", got.DriveBusy)
	}
	// Nothing is asking for a decision, so the Plan slot is empty.
	if got.Job != nil {
		t.Errorf("a converting job appeared in the decision slot: %+v", got.Job)
	}
}

// While a disc is being copied the drive is named, so the page can say what it
// is waiting for rather than simply refusing.
func TestStateNamesWhatHoldsTheDrive(t *testing.T) {
	s := newTestServer(t)
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "ripping", Title: "In the Grey", State: store.StateRunning, Stage: store.StageRip},
	})

	rec := get(t, s, "/api/state")

	var got state
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DriveBusy != "In the Grey" {
		t.Errorf("DriveBusy = %q, want the disc being copied", got.DriveBusy)
	}
}

// Ejecting is refused only by a disc actually in use.
func TestEjectAllowedWhileConverting(t *testing.T) {
	s := newTestServer(t)
	s.Runner.SetCurrentForTest(&pipeline.Job{
		Job: &store.Job{ID: "converting", Title: "Crime 101", State: store.StateRunning, Stage: store.StagePackage},
	})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/eject", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("ejecting was refused while a film was merely being converted: %s", rec.Body.String())
	}
}

// Projects start from any file ARFABIT made: originals beside their films in
// the library and in the folder masters were kept in before, which is still
// read; the films; and the clips. Anything that is not a video is not one.
// Nothing copied yet is an ordinary state, and must come back as an empty list
// rather than as nothing at all.
func TestSourcesWithNothingCopied(t *testing.T) {
	s := newTestServer(t)

	rec := get(t, s, "/api/sources")
	if !strings.Contains(rec.Body.String(), "sources") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// anOriginal puts a file where an original goes, for a test that needs one to
// be listed. It holds nothing a video would.
func anOriginal(t *testing.T, s *Server) string {
	t.Helper()
	title := meta.Title{Name: "x"}
	path := filepath.Join(title.LibraryDir(s.Config.Paths.Library), title.OriginalName())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Blueprints can be made, changed and removed without touching the settings file.
func TestBlueprintsCanBeMadeAndRemoved(t *testing.T) {
	s := newTestServer(t)

	body := strings.NewReader(`{"name":"Small","preset":"medium","crf_bluray":24}`)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/blueprints", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("saving a blueprint returned %d: %s", rec.Code, rec.Body)
	}

	// It appears in the list, marked as one ARFABIT may change.
	listed := get(t, s, "/api/blueprints")
	var reply struct {
		Blueprints []struct {
			Name      string `json:"name"`
			Editable  bool   `json:"editable"`
			CRFBluray int    `json:"crf_bluray"`
			CRFDVD    int    `json:"crf_dvd"`
		} `json:"blueprints"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}

	var small *struct {
		Name      string `json:"name"`
		Editable  bool   `json:"editable"`
		CRFBluray int    `json:"crf_bluray"`
		CRFDVD    int    `json:"crf_dvd"`
	}
	for i := range reply.Blueprints {
		if reply.Blueprints[i].Name == "Small" {
			small = &reply.Blueprints[i]
		}
	}
	if small == nil {
		t.Fatal("the saved blueprint is not in the list")
	}
	if !small.Editable {
		t.Error("a blueprint made here is not offered as changeable")
	}
	if small.CRFBluray != 24 {
		t.Errorf("crf_bluray = %d, want 24", small.CRFBluray)
	}
	// Anything not named keeps the default, so a form about quality does not
	// quietly change something else.
	if small.CRFDVD != s.Config.Defaults.CRFDVD {
		t.Errorf("crf_dvd = %d; a setting nobody mentioned was changed", small.CRFDVD)
	}

	// And it can be removed again.
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/blueprints/Small", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("removing returned %d: %s", rec.Code, rec.Body)
	}
}

// Settings that would fail halfway through a film are refused when saved.
func TestSavingNonsenseIsRefused(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/blueprints",
		strings.NewReader(`{"name":"Broken","preset":"ultrafast"}`)))

	if rec.Code == http.StatusOK {
		t.Error("an unsupported speed was saved")
	}

	var problem map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem["message"], "ultrafast") {
		t.Errorf("the message does not say what was wrong: %q", problem["message"])
	}
}

// A project can be started from line items alone, with no blueprint: trying
// something once should not mean naming it and remembering it forever.
func TestProjectNeedsNoBlueprint(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/project",
		strings.NewReader(`{"source":`+strconv.Quote(anOriginal(t, s))+`,"film":"x","make":"clip","project":{"length":30000000000,
			"items":[{"kind":"video","action":"convert","to":"hevc","crf":26,"preset":"medium","source":0}]}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("a project of line items alone was refused: %s", rec.Body)
	}
	// It stops on its own, the original holding no video; wait for that, so
	// it is not still writing when the test's folder goes.
	for deadline := time.Now().Add(5 * time.Second); len(s.Runner.Active()) > 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.Blueprints.Saved) != 0 {
		t.Error("a project was saved as a blueprint")
	}
}

// A project that could not be made is refused with a sentence saying why.
func TestProjectIsRefusedPlainly(t *testing.T) {
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/project",
		strings.NewReader(`{"source":`+strconv.Quote(anOriginal(t, s))+`,"make":"film","project":{"items":[]}}`)))
	if rec.Code == http.StatusOK {
		t.Fatal("a project with no picture was accepted")
	}
	if !strings.Contains(rec.Body.String(), "needs a picture") {
		t.Errorf("the message does not say what was wrong: %s", rec.Body)
	}
}

// Reading a file needs one, and one that cannot be read says so.
// The edition can be changed on the Plan, or cleared.
func TestPlanEditionCanBeChanged(t *testing.T) {
	s := newTestServer(t)
	job := &pipeline.Job{Job: &store.Job{
		ID: "waiting", State: store.StateWaiting, Stage: store.StagePlan,
		Plan: &store.Plan{Blueprint: "Small", Edition: "Small"},
	}}
	s.Runner.SetCurrentForTest(job)

	for _, edition := range []string{" Director's Cut ", ""} {
		body := strings.NewReader(`{"edition":` + strconv.Quote(edition) + `}`)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if want := strings.TrimSpace(edition); job.Plan.Edition != want {
			t.Errorf("Edition = %q, want %q", job.Plan.Edition, want)
		}
	}

	// Leaving it out of a change leaves it alone.
	job.Plan.Edition = "Kept"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"convert":true}`)))
	if job.Plan.Edition != "Kept" {
		t.Errorf("an unrelated change cleared the edition: %q", job.Plan.Edition)
	}
}

// A blueprint saved without an edition takes its name; one saved with a blank
// edition keeps it blank.
func TestSavedBlueprintEdition(t *testing.T) {
	s := newTestServer(t)

	for _, body := range []string{
		`{"name":"Small","preset":"medium"}`,
		`{"name":"Plain","preset":"medium","edition":""}`,
		`{"name":"Cut","preset":"medium","edition":"Director's Cut"}`,
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/blueprints", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("saving %s returned %d: %s", body, rec.Code, rec.Body)
		}
	}

	for name, want := range map[string]string{"Small": "Small", "Plain": "", "Cut": "Director's Cut"} {
		p, ok := s.Blueprints.Named(s.Config, name)
		if !ok {
			t.Fatalf("%s was not saved", name)
		}
		if p.Edition != want {
			t.Errorf("%s has edition %q, want %q", name, p.Edition, want)
		}
	}
}

// A job waiting for the processor can be moved in the line, and the page is
// told the new order; something that has already started cannot.
func TestMovingInTheLine(t *testing.T) {
	s := newTestServer(t)
	s.Runner.Slots = pipeline.NewSlots(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Runner.Slots.Take(ctx, pipeline.Ticket{ID: "running"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		go func() { _ = s.Runner.Slots.Take(ctx, pipeline.Ticket{ID: id}) }()
		for deadline := time.Now().Add(time.Second); len(s.Runner.Slots.Line()) == 0 || s.Runner.Slots.Line()[len(s.Runner.Slots.Line())-1] != id; {
			if time.Now().After(deadline) {
				t.Fatalf("%s never joined the line", id)
			}
			time.Sleep(time.Millisecond)
		}
	}

	move := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/line", strings.NewReader(body)))
		return rec
	}

	if rec := move(`{"id":"b","to":0}`); rec.Code != http.StatusOK {
		t.Fatalf("moving b: status %d, %s", rec.Code, rec.Body)
	}

	var reply struct {
		Line []string `json:"line"`
	}
	if err := json.Unmarshal(get(t, s, "/api/state").Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if strings.Join(reply.Line, ",") != "b,a" {
		t.Errorf("line = %v, want [b a]", reply.Line)
	}

	if rec := move(`{"id":"running","to":0}`); rec.Code == http.StatusOK {
		t.Error("a job that had already started was moved")
	}
}

// Sound rules made on the page are kept with the blueprint, and rules the
// matching would misread are refused rather than saved.
func TestBlueprintKeepsItsSoundRules(t *testing.T) {
	s := newTestServer(t)

	save := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/blueprints", strings.NewReader(body)))
		return rec
	}

	if rec := save(`{"name":"Surround","sound":{"languages":["eng"],"language_mode":"one","choices":[{"mode":"one","layouts":["7.1","5.1"],"quality":"lossless"}]}}`); rec.Code != http.StatusOK {
		t.Fatalf("saving: status %d, %s", rec.Code, rec.Body)
	}
	if rec := save(`{"name":"Odd","sound":{"choices":[{"mode":"one","layouts":["9.2"]}]}}`); rec.Code == http.StatusOK {
		t.Error("a layout that does not exist was saved")
	}

	var reply struct {
		Blueprints []struct {
			Name  string             `json:"name"`
			Sound *config.SoundRules `json:"sound"`
		} `json:"blueprints"`
	}
	if err := json.Unmarshal(get(t, s, "/api/blueprints").Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	for _, b := range reply.Blueprints {
		if b.Name == "Surround" {
			if b.Sound == nil || len(b.Sound.Choices) != 1 || b.Sound.Choices[0].Quality != "lossless" {
				t.Errorf("rules came back as %+v", b.Sound)
			}
			return
		}
	}
	t.Error("the blueprint was not listed")
}

// The package on a Plan is replaced whole by the one edited on the page, and
// its edition becomes the Plan's, which names the file.
func TestPlanPackageCanBeChanged(t *testing.T) {
	s := newTestServer(t)
	job := &pipeline.Job{Job: &store.Job{
		ID: "waiting", State: store.StateWaiting, Stage: store.StagePlan,
		Plan: &store.Plan{Convert: true, Project: &store.Project{Items: []store.Item{
			{Kind: store.KindVideo, Action: store.ActionCopy},
		}}},
	}}
	s.Runner.SetCurrentForTest(job)

	body := `{"project":{"edition":"Lossless","items":[
		{"kind":"video","action":"convert","to":"hevc","crf":22,"preset":"medium"},
		{"kind":"audio","action":"convert","source":1,"codec":"truehd","to":"flac"}]}}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}

	pkg := job.Plan.Project
	if len(pkg.Items) != 2 || pkg.Items[1].To != "flac" {
		t.Errorf("the package is now %+v", pkg.Items)
	}
	if job.Plan.Edition != "Lossless" {
		t.Errorf("the Plan's edition is %q, want the package's", job.Plan.Edition)
	}
	// The estimate follows the picture the package makes.
	if job.Plan.VideoCopy || job.Plan.CRF != 22 || job.Plan.Preset != "medium" {
		t.Errorf("the estimate still describes the old picture: copy=%v crf=%d preset=%s",
			job.Plan.VideoCopy, job.Plan.CRF, job.Plan.Preset)
	}
}

// Recent tasks are the ones that are over: anything working or waiting is in
// the queue, or on the Plan, already.
func TestRecentTasksAreOnlyFinishedOnes(t *testing.T) {
	s := newTestServer(t)
	for id, state := range map[string]store.State{
		"done": store.StateDone, "stopped": store.StateStopped,
		"running": store.StateRunning, "waiting": store.StateWaiting,
	} {
		if err := s.Store.SaveJob(&store.Job{ID: id, State: state}); err != nil {
			t.Fatal(err)
		}
	}

	var reply struct {
		Recent []store.Job `json:"recent"`
	}
	if err := json.Unmarshal(get(t, s, "/api/state").Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, j := range reply.Recent {
		got[j.ID] = true
	}
	if len(got) != 2 || !got["done"] || !got["stopped"] {
		t.Errorf("recent = %v, want only done and stopped", got)
	}
}

// The page is told whether this computer can read subtitles into text, and
// where it cannot, what to say (§10).
func TestStateSaysWhetherSubtitlesCanBeRead(t *testing.T) {
	s := newTestServer(t)
	s.Runner.OCR = nil
	var reply struct {
		OCR     bool   `json:"ocr"`
		OCRNote string `json:"ocr_note"`
	}
	if err := json.Unmarshal(get(t, s, "/api/state").Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.OCR || !strings.Contains(reply.OCRNote, "cannot be read into text on this computer") {
		t.Errorf("no reader: %+v", reply)
	}
}

// Which subtitle tracks are read with the copy is chosen on the Plan, track
// by track, and kept in the disc's order.
func TestPlanChoosesSubtitlesToRead(t *testing.T) {
	s := newTestServer(t)
	job := &pipeline.Job{Job: &store.Job{
		ID: "waiting", State: store.StateWaiting, Stage: store.StagePlan,
		Plan: &store.Plan{Read: []int{7}, Tracks: []store.Track{
			{Index: 0, Kind: store.KindVideo},
			{Index: 7, Kind: store.KindSubtitle, Lang: "eng"},
			{Index: 9, Kind: store.KindSubtitle, Lang: "eng"},
		}},
	}}
	s.Runner.SetCurrentForTest(job)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"read":{"9":true,"0":true}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fmt.Sprint(job.Plan.Read) != "[7 9]" {
		t.Errorf("read = %v, want [7 9]: the picture is not a subtitle track", job.Plan.Read)
	}

	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"read":{"7":false}}`)))
	if fmt.Sprint(job.Plan.Read) != "[9]" {
		t.Errorf("read = %v, want [9]", job.Plan.Read)
	}
}

// What a drive does when a disc goes in is kept by the drive's name, and a
// blueprint that is not there is refused.
func TestDriveSettingIsKept(t *testing.T) {
	s := newTestServer(t)
	for body, ok := range map[string]bool{
		`{"drive":"BD-RE BU40N","when":"copy"}`:                         true,
		`{"drive":"BD-RE BU40N","when":"blueprint","blueprint":""}`:     true,
		`{"drive":"BD-RE BU40N","when":"blueprint","blueprint":"Gone"}`: false,
		`{"drive":"BD-RE BU40N","when":"sometimes"}`:                    false,
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/drive-setting", strings.NewReader(body)))
		if (rec.Code == http.StatusOK) != ok {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/drive-setting", strings.NewReader(`{"drive":"BD-RE BU40N","when":"copy"}`)))
	if got := s.Store.DriveSettings()["BD-RE BU40N"]; got.When != store.WhenCopy {
		t.Errorf("kept as %+v", got)
	}
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/drive-setting", strings.NewReader(`{"drive":"BD-RE BU40N","when":"nothing"}`)))
	if _, kept := s.Store.DriveSettings()["BD-RE BU40N"]; kept {
		t.Error("doing nothing is kept as a setting")
	}
}

// A disc goes in when a drive holds one it did not before: not when it holds
// the same one still, and not when it is emptied.
func TestNoticingADiscGoIn(t *testing.T) {
	empty := disc.Drive{Name: "D"}
	one := disc.Drive{Name: "D", Loaded: true, Label: "ONE"}
	two := disc.Drive{Name: "D", Loaded: true, Label: "TWO"}
	for _, tc := range []struct {
		before, now disc.Drive
		in          bool
	}{
		{empty, one, true},
		{one, one, false},
		{one, empty, false},
		{one, two, true},
	} {
		if got := len(wentIn([]disc.Drive{tc.before}, []disc.Drive{tc.now})) == 1; got != tc.in {
			t.Errorf("%+v then %+v: went in = %v", tc.before, tc.now, got)
		}
	}
}

// Reading subtitles only starts nothing but OCR tasks, and says plainly why
// when it cannot.
func TestReadSubtitlesOnly(t *testing.T) {
	s := newTestServer(t)
	path := anOriginal(t, s)

	rec := post(t, s, "/api/project", `{"source":`+strconv.Quote(path)+`,"make":"read","read":[]}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "Tick") {
		t.Errorf("nothing chosen: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, s, "/api/project", `{"source":`+strconv.Quote(path)+`,"make":"read","read":[3]}`)
	if rec.Code == http.StatusOK {
		t.Errorf("a file with no subtitles was read: %s", rec.Body)
	}
	if len(s.Runner.Active()) != 0 {
		t.Error("something joined the queue")
	}
}

// Projects start from any file ARFABIT made: originals beside their films in
// the library, the films, and the clips. Anything that is not a video is not one.
func TestSourcesAreListed(t *testing.T) {
	s := newTestServer(t)

	title := meta.Title{Name: "In the Grey", Year: 2026}
	beside := filepath.Join(title.LibraryDir(s.Config.Paths.Library), title.OriginalName())
	film := filepath.Join(title.LibraryDir(s.Config.Paths.Library), title.VideoName(""))
	clip := filepath.Join(s.Config.Paths.Clips, "In the Grey (2026)", "In the Grey (2026) {edition-Lab 001 - Small - 0h01m00s}.mkv")
	other := filepath.Join(s.Config.Paths.Library, "Crime 101 (2025)", "Crime 101 (2025) {edition-Original}.mkv")
	for _, path := range []string{beside, film, clip, other,
		filepath.Join(filepath.Dir(other), "job.json"), filepath.Join(filepath.Dir(film), ".arfabit-piece-x.mkv")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var got struct {
		Sources []source `json:"sources"`
	}
	if err := json.Unmarshal(get(t, s, "/api/sources").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []source{
		{Title: "Crime 101 (2025)", Path: other, Kind: "original"},
		{Title: "In the Grey (2026)", Path: beside, Kind: "original"},
		{Title: "In the Grey (2026)", Path: film, Kind: "film"},
		{Title: "In the Grey (2026)", Path: clip, Kind: "clip"},
	}
	if len(got.Sources) != len(want) {
		t.Fatalf("got %+v", got.Sources)
	}
	for i := range want {
		g := got.Sources[i]
		if g.Title != want[i].Title || g.Path != want[i].Path || g.Kind != want[i].Kind {
			t.Errorf("%d: %+v, want %+v", i, g, want[i])
		}
	}
}

// Reading a file needs one, and one that cannot be read says so.
func TestSourceMustBeReadable(t *testing.T) {
	s := newTestServer(t)

	if rec := get(t, s, "/api/source"); rec.Code == http.StatusOK {
		t.Error("tracks were listed for no file at all")
	}
	rec := get(t, s, "/api/source?path="+url.QueryEscape(anOriginal(t, s)))
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "could not be read") {
		t.Errorf("a file that is not a video: %d %s", rec.Code, rec.Body)
	}

	// ARFABIT listens on the whole network, so it reads nothing but the
	// files it lists, however the path is dressed up.
	missing := filepath.Join(s.Config.Paths.Library, "Gone (2020)", "Gone (2020) {edition-Original}.mkv")
	for _, outside := range []string{missing, "/etc/hosts", filepath.Join(s.Config.Paths.Library, "..", "config.toml"), s.Config.Paths.Library} {
		rec := get(t, s, "/api/source?path="+url.QueryEscape(outside))
		if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "not a file ARFABIT made") {
			t.Errorf("%s was read: %d %s", outside, rec.Code, rec.Body)
		}
	}
}

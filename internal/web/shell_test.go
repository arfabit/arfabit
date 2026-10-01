package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/update"
)

func readShell(t *testing.T, rec *httptest.ResponseRecorder) shellState {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var state shellState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestShellSettingsStartShownAndNotAtLogin(t *testing.T) {
	state := readShell(t, get(t, newTestServer(t), "/api/shell"))

	if !state.ShowIcon || state.StartAtLogin || state.HideOnStart {
		t.Errorf("settings = %+v, want the icon shown, nothing at login, and the page opened on start", state.ShellSettings)
	}
	if state.Login != nil {
		t.Errorf("login = %+v before the app has reported", state.Login)
	}
	if state.Status != "Nothing running" {
		t.Errorf("status = %q", state.Status)
	}
}

func TestShellSettingsChangeOneAtATime(t *testing.T) {
	s := newTestServer(t)

	readShell(t, post(t, s, "/api/shell/settings", `{"start_at_login":true}`))
	readShell(t, post(t, s, "/api/shell/settings", `{"hide_on_start":true}`))
	state := readShell(t, post(t, s, "/api/shell/settings", `{"show_icon":false}`))

	if state.ShowIcon || !state.StartAtLogin || !state.HideOnStart {
		t.Errorf("settings = %+v, want the icon hidden, started at login, and the page not opened on start", state.ShellSettings)
	}

	// Kept in the node's own state, so a restart finds them.
	if got := s.Store.ShellSettings(); got != state.ShellSettings {
		t.Errorf("saved = %+v, sent = %+v", got, state.ShellSettings)
	}
}

func TestLoginReportIsSentBack(t *testing.T) {
	s := newTestServer(t)

	state := readShell(t, post(t, s, "/api/shell/login",
		`{"enabled":false,"state":"requiresApproval"}`))

	if state.Login == nil || state.Login.State != "requiresApproval" || state.Login.Enabled {
		t.Errorf("login = %+v", state.Login)
	}
}

func TestStatusLine(t *testing.T) {
	job := func(stage store.Stage, title string, percent float64) *pipeline.Job {
		return &pipeline.Job{
			Job:      &store.Job{Stage: stage, Title: title},
			Progress: pipeline.Progress{Percent: percent},
		}
	}

	cases := []struct {
		active []*pipeline.Job
		want   string
	}{
		{nil, "Nothing running"},
		{[]*pipeline.Job{job(store.StageRip, "Blade Runner", 42.7)}, "Copying Blade Runner — 42%"},
		{[]*pipeline.Job{job(store.StageScan, "Heat", 0)}, "Reading Heat"},
		{
			[]*pipeline.Job{job(store.StageQueued, "Alien", 0), job(store.StagePackage, "Heat", 10)},
			"Making Heat — 10%, and 1 more",
		},
	}
	for _, c := range cases {
		if got := statusLine(c.active); got != c.want {
			t.Errorf("statusLine = %q, want %q", got, c.want)
		}
	}
}

func TestUpdatesSayWhyTheyAreOff(t *testing.T) {
	s := newTestServer(t)
	s.UpdatesOff = "This is a development copy, built from the source. It does not update itself."

	var state updateState
	_ = json.Unmarshal(get(t, s, "/api/update").Body.Bytes(), &state)
	if state.Off != s.UpdatesOff || state.Status != nil {
		t.Errorf("state = %+v", state)
	}

	if rec := post(t, s, "/api/update/check", ""); rec.Code == http.StatusOK {
		t.Error("a copy that does not update itself checked")
	}
}

func TestUpdateSettingsChange(t *testing.T) {
	s := newTestServer(t)
	s.SetUpdater(&update.Updater{Dir: t.TempDir(), Current: "v0.1.0"})

	rec := post(t, s, "/api/update/settings", `{"install_by_themselves":false}`)
	var state updateState
	_ = json.Unmarshal(rec.Body.Bytes(), &state)

	if state.Settings == nil || !state.Settings.CheckDaily || state.Settings.InstallByThemselves {
		t.Errorf("settings = %+v, want daily checks on and installing by themselves off", state.Settings)
	}
	if got := s.Store.UpdateSettings(); got != *state.Settings {
		t.Errorf("saved = %+v", got)
	}
}

func TestUpdateAndRestartNeedsSomethingStaged(t *testing.T) {
	s := newTestServer(t)
	s.SetUpdater(&update.Updater{Dir: t.TempDir(), Current: "v0.1.0"})
	restarted := false
	s.Restart = func() error { restarted = true; return nil }

	if rec := post(t, s, "/api/update/install", ""); rec.Code == http.StatusOK || restarted {
		t.Errorf("restarted with nothing to install: %d", rec.Code)
	}
}

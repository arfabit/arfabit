package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
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

	if !state.ShowIcon || state.StartAtLogin {
		t.Errorf("settings = %+v, want the icon shown and nothing at login", state.ShellSettings)
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
	state := readShell(t, post(t, s, "/api/shell/settings", `{"show_icon":false}`))

	if state.ShowIcon || !state.StartAtLogin {
		t.Errorf("settings = %+v, want the icon hidden and started at login", state.ShellSettings)
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

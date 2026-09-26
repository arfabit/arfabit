package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/subs"
)

// A finished task with one subtitle of low confidence, in a sidecar.
func lowConfidenceJob(t *testing.T, s *Server) (*store.Job, string) {
	t.Helper()
	dir := filepath.Join(s.Config.Paths.Library, "Film (2026)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "Film (2026).en.srt")
	srt := subs.WriteSRT([]subs.Cue{
		{Start: time.Second, End: 2 * time.Second, Text: "So do I."},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "'Course | am."},
	})
	if err := os.WriteFile(sidecar, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	picture, err := s.Store.SavePicture("job-1", "3-00001.png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	job := &store.Job{ID: "job-1", State: store.StateDone, Sidecars: []string{sidecar},
		LowConfidence: []store.LowConfidence{{
			Sidecar: sidecar, Start: 3 * time.Second, End: 4 * time.Second,
			Text: "'Course | am.", Fast: "'Course l am.", Picture: picture,
		}}}
	if err := s.Store.SaveJob(job); err != nil {
		t.Fatal(err)
	}
	return job, sidecar
}

// What a person chooses is written into the sidecar, that subtitle only,
// and noted on the task.
func TestChoosingALowConfidenceSubtitle(t *testing.T) {
	s := newTestServer(t)
	_, sidecar := lowConfidenceJob(t, s)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-1/low-confidence/0",
		strings.NewReader(`{"text":"'Course I am."}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	data, _ := os.ReadFile(sidecar)
	want := "1\n00:00:01,000 --> 00:00:02,000\nSo do I.\n\n2\n00:00:03,000 --> 00:00:04,000\n'Course I am.\n\n"
	if string(data) != want {
		t.Errorf("sidecar:\n%s", data)
	}
	job, _ := s.Store.LoadJob("job-1")
	if l := job.LowConfidence[0]; !l.Changed || l.Chosen != "'Course I am." {
		t.Errorf("entry = %+v", l)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(sidecar), ".arfabit-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// The picture is served from where the task keeps them, and nowhere else.
func TestLowConfidencePicture(t *testing.T) {
	s := newTestServer(t)
	job, sidecar := lowConfidenceJob(t, s)

	rec := get(t, s, "/api/jobs/job-1/low-confidence/0/picture")
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
	if rec := get(t, s, "/api/jobs/job-1/low-confidence/1/picture"); rec.Code != http.StatusNotFound {
		t.Errorf("a subtitle that is not there: %d", rec.Code)
	}

	job.LowConfidence[0].Picture = sidecar
	if err := s.Store.SaveJob(job); err != nil {
		t.Fatal(err)
	}
	if rec := get(t, s, "/api/jobs/job-1/low-confidence/0/picture"); rec.Code != http.StatusNotFound {
		t.Errorf("a file outside the pictures was served: %d", rec.Code)
	}
}

// "These are fine" marks an OCR task's subtitles as looked at, without
// changing the SRT, and a task still to be checked stays in Recent tasks
// however many have finished since.
func TestSubtitlesCanBeMarkedFine(t *testing.T) {
	s := newTestServer(t)
	job, sidecar := lowConfidenceJob(t, s)
	job.Kind, job.Reading = store.KindOCR, &store.Reading{Stream: 3, Lang: "eng", SRT: sidecar}
	job.Started = time.Now().Add(-time.Hour)
	if err := s.Store.SaveJob(job); err != nil {
		t.Fatal(err)
	}
	for i := range 25 {
		later := &store.Job{ID: fmt.Sprintf("later-%02d", i), State: store.StateDone, Started: time.Now()}
		if err := s.Store.SaveJob(later); err != nil {
			t.Fatal(err)
		}
	}

	listed := func() bool {
		var reply struct {
			Recent []store.Job `json:"recent"`
		}
		if err := json.Unmarshal(get(t, s, "/api/state").Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		for _, j := range reply.Recent {
			if j.ID == job.ID {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Error("subtitles still to be checked scrolled out of Recent tasks")
	}

	before, _ := os.ReadFile(sidecar)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/job-1/fine", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	saved, _ := s.Store.LoadJob("job-1")
	if saved.ToCheck() || !saved.Reading.Fine {
		t.Error("the task is still waiting to be checked")
	}
	if after, _ := os.ReadFile(sidecar); string(after) != string(before) {
		t.Error("the SRT changed")
	}
	if listed() {
		t.Error("a task with nothing left to check is still kept in Recent tasks")
	}

	if rec := post(t, s, "/api/jobs/later-00/fine", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a task that read nothing was marked fine: %d", rec.Code)
	}
}

func post(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

// After a fix beside the original, its OCR task says which films have an
// older copy and brings them up to date; a copy somebody else changed is left
// alone and said so.
func TestCopiesAreBroughtUpToDate(t *testing.T) {
	s := newTestServer(t)
	dir := filepath.Join(s.Config.Paths.Library, "Film (2026)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(dir, "Film (2026) {edition-Original}.en.srt")
	ours, theirs := filepath.Join(dir, "Film (2026).en.srt"), filepath.Join(dir, "Film (2026) {edition-Small}.en.srt")
	for path, text := range map[string]string{from: "fixed", ours: "read", theirs: "somebody's own"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := sha256.Sum256([]byte("read"))
	sum := hex.EncodeToString(read[:])
	for _, job := range []*store.Job{
		{ID: "ocr", Kind: store.KindOCR, State: store.StateDone, Reading: &store.Reading{SRT: from}},
		{ID: "film", Kind: store.KindConvert, State: store.StateDone, Copies: []store.Copy{{From: from, To: ours, SHA256: sum}}},
		{ID: "small", Kind: store.KindConvert, State: store.StateDone, Copies: []store.Copy{{From: from, To: theirs, SHA256: sum}}},
	} {
		if err := s.Store.SaveJob(job); err != nil {
			t.Fatal(err)
		}
	}

	var state struct {
		CopiesOf map[string][]copyState `json:"copies_of"`
	}
	if err := json.Unmarshal(get(t, s, "/api/state").Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(state.CopiesOf["ocr"]); !strings.Contains(got, "behind") || !strings.Contains(got, "changed") {
		t.Errorf("the OCR task's copies: %s", got)
	}

	rec := post(t, s, "/api/jobs/ocr/bring-up-to-date", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var reply struct {
		Updated int      `json:"updated"`
		Left    []string `json:"left"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &reply)
	if reply.Updated != 1 || len(reply.Left) != 1 || reply.Left[0] != theirs {
		t.Errorf("reply = %+v", reply)
	}
	if data, _ := os.ReadFile(ours); string(data) != "fixed" {
		t.Errorf("the film's copy holds %q", data)
	}
	if data, _ := os.ReadFile(theirs); string(data) != "somebody's own" {
		t.Errorf("a copy somebody changed was replaced: %q", data)
	}
	film, _ := s.Store.LoadJob("film")
	fixed := sha256.Sum256([]byte("fixed"))
	if film.Copies[0].SHA256 != hex.EncodeToString(fixed[:]) {
		t.Error("what was written is not recorded")
	}
}

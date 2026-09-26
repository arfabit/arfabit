package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T, node string) *Store {
	t.Helper()
	s, err := New(t.TempDir(), node)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveAndLoadJob(t *testing.T) {
	s := newTestStore(t, "mac-mini")

	job := NewJob("2026-09-23-140000-crime-101")
	job.DiscName = "Crime 101"
	job.Stage = StageRip
	job.Plan = &Plan{Blueprint: "Archive", CRF: 20, Preset: "slow"}

	if err := s.SaveJob(job); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscName != "Crime 101" || got.Stage != StageRip {
		t.Errorf("round trip lost data: %+v", got)
	}
	if got.Plan == nil || got.Plan.CRF != 20 {
		t.Errorf("plan did not survive the round trip: %+v", got.Plan)
	}
	// The store stamps the node so a record says which machine wrote it.
	if got.Node != "mac-mini" {
		t.Errorf("Node = %q, want mac-mini", got.Node)
	}
}

// A node writes only inside its own directory, which is what makes a shared
// folder safe without locking.
func TestJobsAreScopedToTheNode(t *testing.T) {
	s := newTestStore(t, "mac-mini")
	if err := s.SaveJob(NewJob("job-1")); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(s.Root, "nodes", "mac-mini", "jobs", "job-1.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("job was not written to the node's own directory: %v", err)
	}
}

// Every machine sees every machine's jobs by reading, never writing.
func TestAllJobsSpansNodes(t *testing.T) {
	root := t.TempDir()

	mac, err := New(root, "mac-mini")
	if err != nil {
		t.Fatal(err)
	}
	linux, err := New(root, "linux-box")
	if err != nil {
		t.Fatal(err)
	}

	older := NewJob("older")
	older.Started = time.Now().Add(-time.Hour)
	if err := mac.SaveJob(older); err != nil {
		t.Fatal(err)
	}

	newer := NewJob("newer")
	if err := linux.SaveJob(newer); err != nil {
		t.Fatal(err)
	}

	all, err := mac.AllJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d jobs across nodes, want 2", len(all))
	}
	// Newest first.
	if all[0].ID != "newer" {
		t.Errorf("jobs are not newest-first: %s then %s", all[0].ID, all[1].ID)
	}
}

// A hand-edited or half-written record must not hide the others.
func TestUnreadableJobIsSkipped(t *testing.T) {
	s := newTestStore(t, "mac-mini")
	if err := s.SaveJob(NewJob("good")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.JobPath("broken"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs, err := s.Jobs()
	if err != nil {
		t.Fatalf("one broken record stopped the listing: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "good" {
		t.Errorf("got %d jobs, want just the readable one", len(jobs))
	}
}

// Writes are atomic so a reader on a NAS never sees half a record.
func TestWriteAtomicLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "job.json")

	if err := writeAtomic(path, []byte(`{"id":"one"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte(`{"id":"two"}`)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"id":"two"}` {
		t.Errorf("file = %q, want the second write", data)
	}

	// No temporary files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestAppendLibraryIsAppendOnly(t *testing.T) {
	s := newTestStore(t, "mac-mini")

	for _, name := range []string{"Crime 101", "In the Grey"} {
		if err := s.AppendLibrary(LibraryEntry{Title: name, Year: 2025}); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Join(s.Root, "library", "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "Crime 101") {
		t.Errorf("first entry was replaced rather than appended: %s", lines[0])
	}
}

// Job ids become filenames, so they must be readable and sort by time.
func TestNewJobID(t *testing.T) {
	when := time.Date(2026, 9, 23, 14, 30, 0, 0, time.UTC)

	if got, want := NewJobID(when, "THE_SHEEP_DETECTIVES"), "2026-09-23-143000-the-sheep-detectives"; got != want {
		t.Errorf("NewJobID = %q, want %q", got, want)
	}
	if got := NewJobID(when, ""); !strings.HasSuffix(got, "-disc") {
		t.Errorf("an unlabelled disc should still get a usable id: %q", got)
	}
	if got := NewJobID(when, "Alien: Resurrection!"); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("job id contains characters unsafe in a filename: %q", got)
	}
}

// A record written before an Original was called a master, and a Project a
// package, reads as it would be written now.
func TestRecordsFromBeforeReadTheSame(t *testing.T) {
	s, err := New(t.TempDir(), "node")
	if err != nil {
		t.Fatal(err)
	}
	old := `{"id":"old","state":"done","master":"/m/Film (2020)/t00.mkv",
		"package":{"edition":"Small","items":[{"kind":"video","action":"copy","source":0}]},
		"plan":{"package":{"edition":"Plan"}}}`
	if err := os.WriteFile(s.JobPath("old"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	j, err := s.LoadJob("old")
	if err != nil {
		t.Fatal(err)
	}
	if j.Original != "/m/Film (2020)/t00.mkv" {
		t.Errorf("Original = %q", j.Original)
	}
	if j.Project == nil || j.Project.Edition != "Small" || len(j.Project.Items) != 1 {
		t.Errorf("Project = %+v", j.Project)
	}
	if j.Plan == nil || j.Plan.Project == nil || j.Plan.Project.Edition != "Plan" {
		t.Errorf("the Plan's project = %+v", j.Plan)
	}

	listed, _ := s.Jobs()
	if len(listed) != 1 || listed[0].Original == "" {
		t.Errorf("the list reads it differently: %+v", listed)
	}
}

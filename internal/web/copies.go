package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

// A film's SRTs are copies of those beside its original. Once one beside the
// original is fixed, its copies are out of date, and the page offers to bring
// them up to date (§10).

// copyState is how one copy stands, for the page.
type copyState struct {
	// Task is the task that made the copy, and From and To the SRT it came
	// from and the one it is.
	Task  string `json:"task"`
	From  string `json:"from"`
	To    string `json:"to"`
	State string `json:"state"`
}

// copiesOf lists how every copy stands: by the task that made it, and by
// the SRT it was taken from.
func (s *Server) copiesOf(all []*store.Job) (byTask, byFrom map[string][]copyState) {
	byTask, byFrom = map[string][]copyState{}, map[string][]copyState{}
	for _, job := range all {
		for _, c := range job.Copies {
			state := copyState{Task: job.ID, From: c.From, To: c.To, State: pipeline.CopyState(&s.hashes, c)}
			byTask[job.ID] = append(byTask[job.ID], state)
			key := filepath.Clean(c.From)
			byFrom[key] = append(byFrom[key], state)
		}
	}
	return byTask, byFrom
}

// handleBringUpToDate replaces copies that are out of date with the SRT
// beside the original as it is now: a package task's own copies, or every
// copy of an OCR task's SRT. A copy somebody else has changed is left alone,
// and said so.
func (s *Server) handleBringUpToDate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}
	asked, err := s.Store.LoadJob(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Which copies: this task's own, or every copy of what it read.
	want := func(job *store.Job, c store.Copy) bool { return job.ID == asked.ID }
	if asked.Kind == store.KindOCR && asked.Reading != nil {
		srt := filepath.Clean(asked.Reading.SRT)
		want = func(_ *store.Job, c store.Copy) bool { return filepath.Clean(c.From) == srt }
	}

	// Only this node's own tasks are changed: a node writes only in its own
	// folder (§6).
	jobs, err := s.Store.Jobs()
	if err != nil {
		writeError(w, "ARFABIT could not read its tasks.", err)
		return
	}

	var (
		updated int
		left    []string
		failed  []string
	)
	for _, job := range jobs {
		changed := false
		for i := range job.Copies {
			c := &job.Copies[i]
			if !want(job, *c) {
				continue
			}
			switch pipeline.CopyState(&s.hashes, *c) {
			case pipeline.CopyChanged:
				left = append(left, c.To)
				continue
			case pipeline.CopyBehind:
			default:
				continue
			}
			switch err := pipeline.BringUpToDate(c); {
			case errors.Is(err, pipeline.ErrCopyChanged):
				left = append(left, c.To)
			case err != nil:
				failed = append(failed, filepath.Base(c.To)+": "+err.Error())
			default:
				updated++
				changed = true
			}
		}
		if changed {
			if err := s.Store.SaveJob(job); err != nil {
				failed = append(failed, job.ID+": "+err.Error())
			}
		}
	}

	if len(failed) > 0 {
		writeError(w, "ARFABIT did not finish bringing the subtitles up to date.", errors.New(strings.Join(failed, "\n")))
		return
	}
	s.events.send("job", map[string]string{"id": asked.ID})
	writeJSON(w, map[string]any{"updated": updated, "left": left})
}

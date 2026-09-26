package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/subs"
)

// Subtitles of low confidence (§10): each is shown with its picture, and a
// person chooses what it says — the first reading, the second, or their own —
// which is written into its sidecar. Nothing changes until they choose.

// lowConfidence finds the job and the subtitle a request is about. Only a
// finished job's are changed: one still working is still writing them.
func (s *Server) lowConfidence(w http.ResponseWriter, r *http.Request) (*store.Job, int, bool) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return nil, 0, false
	}
	job, err := s.Store.LoadJob(id)
	if err != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(job.LowConfidence) {
		http.NotFound(w, r)
		return nil, 0, false
	}
	return job, n, true
}

// handleLowConfidencePicture shows a subtitle as it is on the disc. It reads
// only from where the job keeps its pictures.
func (s *Server) handleLowConfidencePicture(w http.ResponseWriter, r *http.Request) {
	job, n, ok := s.lowConfidence(w, r)
	if !ok {
		return
	}
	path := job.LowConfidence[n].Picture
	rel, err := filepath.Rel(filepath.Dir(s.Store.PicturePath(job.ID, "x")), path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(data)
}

// handleChooseLowConfidence writes what a person chose into the sidecar.
func (s *Server) handleChooseLowConfidence(w http.ResponseWriter, r *http.Request) {
	job, n, ok := s.lowConfidence(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that request.", err)
		return
	}
	if job.State != store.StateDone {
		writeError(w, "Subtitles can be changed once the task is done.", nil)
		return
	}

	entry := &job.LowConfidence[n]
	if !slices.Contains(job.Sidecars, entry.Sidecar) {
		writeError(w, "That subtitle file is not one this task made.", nil)
		return
	}
	data, err := os.ReadFile(entry.Sidecar)
	if err != nil {
		writeError(w, "The subtitle file is no longer where ARFABIT put it.", err)
		return
	}
	cues, err := subs.ParseSRT(string(data))
	if err != nil {
		writeError(w, "ARFABIT could not read the subtitle file, so it left it as it is.", err)
		return
	}

	text := strings.TrimSpace(strings.ReplaceAll(req.Text, "\r\n", "\n"))
	cues = subs.SetCue(cues, entry.Start, entry.End, text)
	if err := replaceFile(entry.Sidecar, []byte(subs.WriteSRT(cues))); err != nil {
		writeError(w, "ARFABIT could not save the subtitle file.", err)
		return
	}

	entry.Changed, entry.Chosen = true, text
	if err := s.Store.SaveJob(job); err != nil {
		writeError(w, "The subtitle file is saved, but ARFABIT could not note it on the task.", err)
		return
	}
	writeJSON(w, map[string]any{"chosen": text})
}

// handleSubtitlesAreFine marks an OCR task's subtitles of low confidence as
// looked at and fine as they are, without going through them one by one.
// Nothing in the SRT changes.
func (s *Server) handleSubtitlesAreFine(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}
	job, err := s.Store.LoadJob(id)
	if err != nil || job.Kind != store.KindOCR || job.Reading == nil {
		http.NotFound(w, r)
		return
	}
	if job.State != store.StateDone {
		writeError(w, "Subtitles can be marked fine once they are read.", nil)
		return
	}
	job.Reading.Fine = true
	if err := s.Store.SaveJob(job); err != nil {
		writeError(w, "ARFABIT could not note that on the task.", err)
		return
	}
	s.events.send("job", map[string]string{"id": job.ID})
	writeJSON(w, map[string]bool{"fine": true})
}

// replaceFile writes a file whole under another name and renames it into
// place, so it is never seen half written.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".arfabit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

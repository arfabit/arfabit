package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/drive"
	"github.com/arfabit/arfabit/internal/lab"
	"github.com/arfabit/arfabit/internal/pipeline"
)

// handleDriveHealth reports how the drive is being reached and how fast it has
// been observed to read.
//
// The access mode matters more than anything else about the setup: it is the
// difference between a film taking forty minutes and taking four hours.
func (s *Server) handleDriveHealth(w http.ResponseWriter, r *http.Request) {
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		writeError(w, "The drive is busy with "+busy.Title+". Ask again once that disc is out.", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	health, err := s.Backend.CheckHealth(ctx)
	if err != nil && len(health) == 0 {
		writeError(w, "ARFABIT could not ask the drive anything.", err)
		return
	}

	type driveReport struct {
		Name        string  `json:"name"`
		Mounted     bool    `json:"mounted"`
		MountNote   string  `json:"mount_note,omitempty"`
		Access      string  `json:"access"`
		Fast        bool    `json:"fast"`
		Explanation string  `json:"explanation"`
		Observed    float64 `json:"observed_mb_per_second"`
		Samples     int     `json:"samples"`
		Device      string  `json:"device"`
	}

	reports := make([]driveReport, 0, len(health))
	for _, h := range health {
		report := driveReport{
			Name:        h.Drive.Name,
			Device:      h.Drive.Device,
			Access:      string(h.Access),
			Fast:        h.Fast(),
			Explanation: h.Explain(),
		}

		// The system holding the disc open is the usual reason a drive reads
		// slowly, and it is something a person can act on.
		if mount := drive.Check(ctx, h.Drive.Device); mount.Mounted {
			report.Mounted = true
			report.MountNote = mount.Describe()
		}

		// What this drive has actually managed, which beats any claim about
		// what it ought to manage.
		if stats := s.Runner.Calibration.Drives[h.Drive.Device]; stats != nil {
			for kind, rate := range stats.MBPerSecond {
				if rate > report.Observed {
					report.Observed = rate
					report.Samples = stats.Samples[kind]
				}
			}
		}
		reports = append(reports, report)
	}

	writeJSON(w, map[string]any{"drives": reports})
}

// handleFreeDrive asks the operating system to let go of the disc.
func (s *Server) handleFreeDrive(w http.ResponseWriter, r *http.Request) {
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		writeError(w, "The drive is busy with "+busy.Title+".", nil)
		return
	}

	device := ""
	for _, d := range s.Drives() {
		device = d.Device
		break
	}
	if device == "" {
		writeError(w, "There is no disc drive to free.", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	state := drive.Unmount(ctx, device)

	switch {
	case !state.Present:
		writeError(w, "The disc came out when your computer let go of it. Put it back in — ARFABIT will read it as it is.", nil)
	case state.Mounted:
		writeError(w, "Your computer would not let go of the disc.", nil)
	default:
		writeJSON(w, map[string]any{"freed": true})
	}
}

// handleMasters lists the copies available to experiment on.
func (s *Server) handleMasters(w http.ResponseWriter, r *http.Request) {
	type master struct {
		Title    string  `json:"title"`
		Path     string  `json:"path"`
		Size     int64   `json:"size"`
		Duration float64 `json:"duration"`
	}

	var masters []master

	entries, err := os.ReadDir(s.Config.Paths.Masters)
	if err != nil {
		writeJSON(w, map[string]any{"masters": masters})
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Config.Paths.Masters, e.Name())

		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) != ".mkv" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			masters = append(masters, master{
				Title: e.Name(),
				Path:  filepath.Join(dir, f.Name()),
				Size:  info.Size(),
			})
		}
	}

	writeJSON(w, map[string]any{"masters": masters})
}

// handleLabClips lists the clips already made.
//
// The folder is the record, not a list held in memory: clips outlive the
// program, and somebody coming back tomorrow should find what they made
// yesterday rather than an empty table.
func (s *Server) handleLabClips(w http.ResponseWriter, r *http.Request) {
	type clip struct {
		Film string    `json:"film"`
		Name string    `json:"name"`
		Path string    `json:"path"`
		Size int64     `json:"size"`
		Made time.Time `json:"made"`
	}

	clips := []clip{}

	// A folder per film, as everywhere else, so the listing groups the way
	// the folder does.
	films, err := os.ReadDir(s.Config.Paths.Lab)
	if err == nil {
		for _, film := range films {
			if !film.IsDir() {
				continue
			}
			dir := filepath.Join(s.Config.Paths.Lab, film.Name())

			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || filepath.Ext(e.Name()) != ".mp4" {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				clips = append(clips, clip{
					Film: film.Name(),
					Name: editionOf(e.Name()),
					Path: filepath.Join(dir, e.Name()),
					Size: info.Size(),
					Made: info.ModTime(),
				})
			}
		}
	}

	// Newest first: the ones just made are the ones being judged.
	sort.Slice(clips, func(a, b int) bool { return clips[a].Made.After(clips[b].Made) })

	writeJSON(w, map[string]any{
		"folder": s.Config.Paths.Lab,
		"clips":  clips,
	})
}

// editionOf pulls the edition out of a clip's filename, which is the part
// that says what was tried.
func editionOf(name string) string {
	start := strings.Index(name, "{edition-")
	if start < 0 {
		return strings.TrimSuffix(name, ".mp4")
	}

	tag := name[start+len("{edition-"):]
	if end := strings.Index(tag, "}"); end >= 0 {
		tag = tag[:end]
	}
	return tag
}

// handleLab puts a set of test clips in the queue.
//
// The work itself belongs to the runner, so that a lab run is a job like any
// other: it waits its turn at the processor, keeps a log, can be stopped, and
// shows up in the queue beside the discs.
func (s *Server) handleLab(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Master   string     `json:"master"`
		Film     string     `json:"film"`
		At       float64    `json:"at"`
		Length   float64    `json:"length"`
		Settings []lab.Clip `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that request.", err)
		return
	}

	job, err := s.Runner.StartLab(context.Background(), pipeline.LabRequest{
		Master:    req.Master,
		Film:      req.Film,
		At:        time.Duration(req.At * float64(time.Second)),
		Length:    time.Duration(req.Length * float64(time.Second)),
		Settings:  req.Settings,
		OutputDir: s.Config.Paths.Lab,
	})
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]string{"job": job.ID})
}

// capitalise makes a sentence of a message that was written as a fragment.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

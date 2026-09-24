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

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/drive"
	"github.com/arfabit/arfabit/internal/ffmpeg"
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

// handleTranscode renders a copy under the chosen profiles.
//
// A stretch of the film becomes clips to compare; the whole of it becomes
// films, one per profile, each an edition in the library.
func (s *Server) handleTranscode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Master   string   `json:"master"`
		Film     string   `json:"film"`
		At       float64  `json:"at"`
		Length   float64  `json:"length"`
		Profiles []string `json:"profiles"`
		Audio    []int    `json:"audio"`

		// Custom is a one-off used for this job and not kept.
		Custom *struct {
			Name            string  `json:"name"`
			Preset          *string `json:"preset"`
			CRFUHD          *int    `json:"crf_uhd"`
			CRFBluray       *int    `json:"crf_bluray"`
			CRFDVD          *int    `json:"crf_dvd"`
			AudioBitrate    *string `json:"audio_bitrate"`
			AllowUHDCopy    *bool   `json:"allow_uhd_copy"`
			CopyNativeAudio *bool   `json:"copy_native_audio"`
		} `json:"custom"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that request.", err)
		return
	}

	var custom *config.Profile
	if req.Custom != nil {
		p := applyProfileForm(s.Config.Profile, req.Custom.Name, req.Custom.Preset,
			req.Custom.CRFUHD, req.Custom.CRFBluray, req.Custom.CRFDVD,
			req.Custom.AudioBitrate, req.Custom.AllowUHDCopy, req.Custom.CopyNativeAudio)
		custom = &p
	}

	job, err := s.Runner.StartTranscode(context.Background(), pipeline.LabRequest{
		Master:     req.Master,
		Film:       req.Film,
		At:         time.Duration(req.At * float64(time.Second)),
		Length:     time.Duration(req.Length * float64(time.Second)),
		Profiles:   req.Profiles,
		Audio:      req.Audio,
		Custom:     custom,
		Lookup:     func(name string) (config.Profile, bool) { return s.Profiles.Named(s.Config, name) },
		LabDir:     s.Config.Paths.Lab,
		LibraryDir: s.Config.Paths.Library,
	})
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]string{"job": job.ID})
}

// handleProfiles lists the named settings available to choose between.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	type reply struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Default     bool   `json:"default"`
		Editable    bool   `json:"editable"`
		Source      string `json:"source"`

		// The settings themselves, so the form can be filled in from one.
		Preset          string `json:"preset"`
		CRFUHD          int    `json:"crf_uhd"`
		CRFBluray       int    `json:"crf_bluray"`
		CRFDVD          int    `json:"crf_dvd"`
		AudioBitrate    string `json:"audio_bitrate"`
		AllowUHDCopy    bool   `json:"allow_uhd_copy"`
		CopyNativeAudio bool   `json:"copy_native_audio"`
	}

	all := s.Profiles.All(s.Config)
	out := make([]reply, 0, len(all))

	for _, p := range all {
		out = append(out, reply{
			Name:            p.Name,
			Description:     p.Describe(),
			Default:         p.Name == s.Config.Profile.Name,
			Editable:        p.Editable,
			Source:          p.Source,
			Preset:          p.Preset,
			CRFUHD:          p.CRFUHD,
			CRFBluray:       p.CRFBluray,
			CRFDVD:          p.CRFDVD,
			AudioBitrate:    p.AudioBitrate,
			AllowUHDCopy:    p.AllowUHDCopy,
			CopyNativeAudio: p.CopyNativeAudio,
		})
	}

	writeJSON(w, map[string]any{"profiles": out})
}

// handleSaveProfile makes or changes a profile.
func (s *Server) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := readProfileForm(r, s.Config.Profile)
	if err != nil {
		writeError(w, "ARFABIT could not read that.", err)
		return
	}

	saved, err := s.Profiles.Save(profile)
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]any{"name": saved.Name})
}

// handleDeleteProfile removes a profile.
//
// Only profiles made here can be removed: one written in the settings file
// belongs to whoever wrote it, and the place to remove it is there.
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := s.Profiles.Delete(name); err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]bool{"removed": true})
}

// readProfileForm reads a profile from a request, filling in anything not
// given from the default.
//
// Starting from the default rather than from nothing means a form that asks
// about quality does not silently turn off subtitles.
func readProfileForm(r *http.Request, base config.Profile) (config.Profile, error) {
	var form struct {
		Name            string  `json:"name"`
		Preset          *string `json:"preset"`
		CRFUHD          *int    `json:"crf_uhd"`
		CRFBluray       *int    `json:"crf_bluray"`
		CRFDVD          *int    `json:"crf_dvd"`
		AudioBitrate    *string `json:"audio_bitrate"`
		AllowUHDCopy    *bool   `json:"allow_uhd_copy"`
		CopyNativeAudio *bool   `json:"copy_native_audio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		return base, err
	}

	return applyProfileForm(base, form.Name, form.Preset, form.CRFUHD, form.CRFBluray,
		form.CRFDVD, form.AudioBitrate, form.AllowUHDCopy, form.CopyNativeAudio), nil
}

// applyProfileForm lays whatever was given over a starting point.
func applyProfileForm(
	base config.Profile,
	name string,
	preset *string,
	crfUHD, crfBluray, crfDVD *int,
	bitrate *string,
	allowUHDCopy, copyNativeAudio *bool,
) config.Profile {
	p := base
	if name != "" {
		p.Name = name
	}
	if preset != nil {
		p.Preset = *preset
	}
	if crfUHD != nil {
		p.CRFUHD = *crfUHD
	}
	if crfBluray != nil {
		p.CRFBluray = *crfBluray
	}
	if crfDVD != nil {
		p.CRFDVD = *crfDVD
	}
	if bitrate != nil {
		p.AudioBitrate = *bitrate
	}
	if allowUHDCopy != nil {
		p.AllowUHDCopy = *allowUHDCopy
	}
	if copyNativeAudio != nil {
		p.CopyNativeAudio = *copyNativeAudio
	}
	return p
}

// handleMasterTracks lists what is inside a master, so tracks can be chosen.
//
// A master holds everything the disc had, which is the point of keeping it.
// A file for a television usually wants a few of those and not the rest.
func (s *Server) handleMasterTracks(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("master")
	if path == "" {
		writeError(w, "No master was given.", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	info, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		writeError(w, "That master could not be read.", err)
		return
	}

	type track struct {
		Index    int    `json:"index"`
		Kind     string `json:"kind"`
		Label    string `json:"label"`
		Lang     string `json:"lang"`
		Channels int    `json:"channels"`
		Selected bool   `json:"selected"`

		// Carriable says whether ARFABIT can put this into the finished file
		// at all. Picture subtitles cannot be, yet.
		Carriable bool   `json:"carriable"`
		Note      string `json:"note,omitempty"`
	}

	tracks := []track{}

	for _, a := range info.StreamsOfKind("audio") {
		tracks = append(tracks, track{
			Index:     a.Index,
			Kind:      "audio",
			Label:     pipeline.DescribeStream(a),
			Lang:      a.Lang,
			Channels:  a.Channels,
			Carriable: true,
			Selected:  wantedLanguage(a.Lang, s.Config.Profile.SubLanguages),
		})
	}

	for _, sub := range info.StreamsOfKind("subtitle") {
		tracks = append(tracks, track{
			Index:     sub.Index,
			Kind:      "subtitle",
			Label:     pipeline.DescribeStream(sub),
			Lang:      sub.Lang,
			Carriable: false,
			Note:      "ARFABIT cannot read picture subtitles into text yet, so these cannot be carried across.",
		})
	}

	writeJSON(w, map[string]any{"tracks": tracks})
}

// wantedLanguage reports whether a language is one the settings ask for.
func wantedLanguage(lang string, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	for _, w := range wanted {
		if strings.EqualFold(lang, w) {
			return true
		}
	}
	return false
}

// capitalise makes a sentence of a message that was written as a fragment.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

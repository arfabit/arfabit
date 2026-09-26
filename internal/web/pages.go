package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/drive"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
)

// handleDriveHealth reports how the drive is being reached and how fast it has
// been observed to read.
//
// The access mode matters more than anything else about the setup: it is the
// difference between a film taking forty minutes and taking four hours.
func (s *Server) handleDriveHealth(w http.ResponseWriter, r *http.Request) {
	if busy := s.Runner.DriveIsBusy(); busy != nil {
		writeError(w, "The drive is busy with "+busy.Name()+".", nil)
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
		// Speeds are kept under the drive's name.
		stats := s.Runner.Calibration.Drives[pipeline.DriveKey(h.Drive.Name, h.Drive.Device)]
		if stats != nil {
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
		writeError(w, "The drive is busy with "+busy.Name()+".", nil)
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

// source is a file a project can start from: an original, or any other file
// ARFABIT made, a film or a clip.
type source struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`

	// Kind is "original", "film" or "clip".
	Kind string `json:"kind"`
}

// sources lists every file a project can start from: originals beside their
// films in the library (§6), the films, and the clips.
func (s *Server) sources() []source {
	var found []source
	video := func(name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		return !strings.HasPrefix(name, ".") && (ext == meta.VideoExt || ext == ".mp4")
	}
	each := func(root string, add func(folder, name, path string)) {
		if root == "" {
			return
		}
		folders, _ := os.ReadDir(root)
		for _, folder := range folders {
			if !folder.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(root, folder.Name()))
			for _, f := range files {
				if !f.IsDir() && video(f.Name()) {
					add(folder.Name(), f.Name(), filepath.Join(root, folder.Name(), f.Name()))
				}
			}
		}
	}
	add := func(title, path, kind string) {
		if info, err := os.Stat(path); err == nil {
			found = append(found, source{Title: title, Path: path, Size: info.Size(), Kind: kind})
		}
	}

	each(s.Config.Paths.Library, func(folder, name, path string) {
		if meta.IsOriginal(name) {
			add(folder, path, "original")
		} else {
			add(folder, path, "film")
		}
	})
	each(s.Config.Paths.Clips, func(folder, name, path string) { add(folder, path, "clip") })

	order := map[string]int{"original": 0, "film": 1, "clip": 2}
	sort.SliceStable(found, func(a, b int) bool {
		if order[found[a].Kind] != order[found[b].Kind] {
			return order[found[a].Kind] < order[found[b].Kind]
		}
		if found[a].Title != found[b].Title {
			return found[a].Title < found[b].Title
		}
		return found[a].Path < found[b].Path
	})
	return found
}

// handleSources lists the files there are to start a project from.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"sources": s.sources()})
}

// handleBlueprints lists the named settings available to choose between.
func (s *Server) handleBlueprints(w http.ResponseWriter, r *http.Request) {
	type reply struct {
		Name        string `json:"name"`
		Edition     string `json:"edition"`
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

		// Sound is the blueprint's sound rules, or null for the usual
		// stereo-first choice.
		Sound *config.SoundRules `json:"sound"`

		KeepPicture bool   `json:"keep_picture"`
		TrueHD      string `json:"truehd"`

		KeepSubtitlePictures bool `json:"keep_subtitle_pictures"`
	}

	all := s.Blueprints.All(s.Config)
	out := make([]reply, 0, len(all))

	for _, p := range all {
		out = append(out, reply{
			Name:            p.Name,
			Edition:         p.Edition,
			Description:     p.Describe(),
			Default:         p.Name == s.Blueprints.DefaultName(s.Config),
			Editable:        p.Editable,
			Source:          p.Source,
			Preset:          p.Preset,
			CRFUHD:          p.CRFUHD,
			CRFBluray:       p.CRFBluray,
			CRFDVD:          p.CRFDVD,
			AudioBitrate:    p.AudioBitrate,
			AllowUHDCopy:    p.AllowUHDCopy,
			CopyNativeAudio: p.CopyNativeAudio,
			Sound:           p.Sound,
			KeepPicture:     p.KeepPicture,
			TrueHD:          orKeep(p.TrueHD),

			KeepSubtitlePictures: p.KeepSubtitlePictures,
		})
	}

	// The defaults are always there to choose, blueprint or not. They are
	// used by default when no blueprint is.
	def := s.Config.Defaults
	writeJSON(w, map[string]any{
		"blueprints": out,
		"defaults": reply{
			Description:     def.Describe(),
			Default:         s.Blueprints.DefaultName(s.Config) == "",
			Source:          "your settings file",
			Preset:          def.Preset,
			CRFUHD:          def.CRFUHD,
			CRFBluray:       def.CRFBluray,
			CRFDVD:          def.CRFDVD,
			AudioBitrate:    def.AudioBitrate,
			AllowUHDCopy:    def.AllowUHDCopy,
			CopyNativeAudio: def.CopyNativeAudio,
			Sound:           def.Sound,
			KeepPicture:     def.KeepPicture,
			TrueHD:          orKeep(def.TrueHD),

			KeepSubtitlePictures: def.KeepSubtitlePictures,
		},
	})
}

// handleDefaultBlueprint chooses which blueprint new Plans start from. An
// empty name chooses none, so they start from the defaults.
func (s *Server) handleDefaultBlueprint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that.", err)
		return
	}

	if err := s.Blueprints.SetDefault(s.Config, req.Name); err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]string{"default": req.Name})
}

// handleSaveBlueprint makes or changes a blueprint.
func (s *Server) handleSaveBlueprint(w http.ResponseWriter, r *http.Request) {
	blueprint, err := readBlueprintForm(r, s.Config.Plain())
	if err != nil {
		writeError(w, "ARFABIT could not read that.", err)
		return
	}

	saved, err := s.Blueprints.Save(blueprint)
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]any{"name": saved.Name})
}

// handleDeleteBlueprint removes a blueprint.
func (s *Server) handleDeleteBlueprint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := s.Blueprints.Delete(s.Config, name); err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]bool{"removed": true})
}

// readBlueprintForm reads a blueprint from a request, filling in anything not
// given from the default.
//
// Starting from the default rather than from nothing means a form that asks
// about quality does not silently turn off subtitles.
func readBlueprintForm(r *http.Request, base config.Blueprint) (config.Blueprint, error) {
	var form struct {
		Name            string  `json:"name"`
		Edition         *string `json:"edition"`
		Preset          *string `json:"preset"`
		CRFUHD          *int    `json:"crf_uhd"`
		CRFBluray       *int    `json:"crf_bluray"`
		CRFDVD          *int    `json:"crf_dvd"`
		AudioBitrate    *string `json:"audio_bitrate"`
		AllowUHDCopy    *bool   `json:"allow_uhd_copy"`
		CopyNativeAudio *bool   `json:"copy_native_audio"`

		// Sound replaces whatever rules there were. Null means none.
		Sound *config.SoundRules `json:"sound"`

		KeepPicture *bool   `json:"keep_picture"`
		TrueHD      *string `json:"truehd"`

		KeepSubtitlePictures *bool `json:"keep_subtitle_pictures"`
	}
	if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
		return base, err
	}
	if err := checkSoundRules(form.Sound); err != nil {
		return base, err
	}

	p := applyBlueprintForm(base, form.Name, form.Edition, form.Preset, form.CRFUHD, form.CRFBluray,
		form.CRFDVD, form.AudioBitrate, form.AllowUHDCopy, form.CopyNativeAudio)
	p.Sound = form.Sound
	if form.KeepPicture != nil {
		p.KeepPicture = *form.KeepPicture
	}
	if form.KeepSubtitlePictures != nil {
		p.KeepSubtitlePictures = *form.KeepSubtitlePictures
	}
	if form.TrueHD != nil {
		switch *form.TrueHD {
		case config.TrueHDKeep, config.TrueHDFLAC, config.TrueHDBoth:
			p.TrueHD = *form.TrueHD
		default:
			return base, fmt.Errorf("%q is not a choice for TrueHD", *form.TrueHD)
		}
	}
	return p, nil
}

// orKeep reads a blueprint saved before the TrueHD choice existed as keeping
// it, which is what those blueprints did.
func orKeep(truehd string) string {
	if truehd == "" {
		return config.TrueHDKeep
	}
	return truehd
}

// checkSoundRules refuses rules the matching would misread, rather than
// saving something that quietly does something else.
func checkSoundRules(rules *config.SoundRules) error {
	if rules == nil {
		return nil
	}
	if len(rules.Choices) == 0 {
		return errors.New("sound rules need at least one choice")
	}
	if !slices.Contains([]string{"", config.SoundOne, config.SoundAll}, rules.LanguageMode) {
		return fmt.Errorf("%q is not a way of choosing languages", rules.LanguageMode)
	}
	for _, c := range rules.Choices {
		if !slices.Contains([]string{config.SoundOne, config.SoundAll}, c.Mode) {
			return fmt.Errorf("%q is not a way of choosing tracks", c.Mode)
		}
		if !slices.Contains([]string{"", "lossless", "lossy"}, c.Quality) {
			return fmt.Errorf("%q is not a quality", c.Quality)
		}
		for _, layout := range c.Layouts {
			if !slices.Contains([]string{"7.1", "5.1", "stereo"}, layout) {
				return fmt.Errorf("%q is not a layout", layout)
			}
		}
	}
	return nil
}

// applyBlueprintForm lays whatever was given over a starting point.
func applyBlueprintForm(
	base config.Blueprint,
	name string,
	edition *string,
	preset *string,
	crfUHD, crfBluray, crfDVD *int,
	bitrate *string,
	allowUHDCopy, copyNativeAudio *bool,
) config.Blueprint {
	p := base
	if name != "" {
		p.Name = name
	}

	// An edition left out starts as the name. One given, even blank, is kept
	// as given: a blank edition is a choice.
	p.Edition = p.Name
	if edition != nil {
		p.Edition = strings.TrimSpace(*edition)
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

// handleResume starts an interrupted job again from its copy.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that.", err)
		return
	}

	job, err := s.Runner.ResumeJob(req.ID)
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}

	writeJSON(w, map[string]string{"job": job.ID})
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

// isSource reports whether a path is one of the files listed to start a
// project from, which is all these endpoints read. ARFABIT listens on the
// whole network, so a path is not taken on trust.
func (s *Server) isSource(path string) bool {
	if path == "" {
		return false
	}
	for _, o := range s.sources() {
		if filepath.Clean(o.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

// handleSource says what a file holds, track by track, and what each would
// cost on the television (§4), so a project can be planned from it.
func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, "No file was given.", nil)
		return
	}
	if !s.isSource(path) {
		writeError(w, "That is not a file ARFABIT made.", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	info, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		writeError(w, "That file could not be read.", err)
		return
	}

	writeJSON(w, map[string]any{
		"tracks":   pipeline.OriginalTracks(info),
		"duration": info.Duration,
	})
}

// handleFillProject fills a project in from a blueprint, or from the
// defaults, against what a file holds. Nothing is started: the line items
// come back to be looked at and changed.
func (s *Server) handleFillProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source    string `json:"source"`
		Blueprint string `json:"blueprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that request.", err)
		return
	}
	if !s.isSource(req.Source) {
		writeError(w, "That is not a file ARFABIT made.", nil)
		return
	}

	blueprint := s.Config.Plain()
	if req.Blueprint != "" {
		named, ok := s.Blueprints.Named(s.Config, req.Blueprint)
		if !ok {
			writeError(w, fmt.Sprintf("There is no blueprint called %s.", req.Blueprint), nil)
			return
		}
		blueprint = named
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	info, err := ffmpeg.Probe(ctx, req.Source)
	if err != nil {
		writeError(w, "That file could not be read.", err)
		return
	}

	writeJSON(w, map[string]any{"project": pipeline.Recipe(pipeline.OriginalTracks(info), blueprint, s.Runner.OCR != nil)})
}

// handleStartProject starts a project from a file ARFABIT made: reading
// some of its subtitles into text, each track a task of its own, or making a
// film or a clip from it.
func (s *Server) handleStartProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source  string        `json:"source"`
		Film    string        `json:"film"`
		Make    string        `json:"make"`
		Read    []int         `json:"read"`
		Project store.Project `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "ARFABIT could not read that request.", err)
		return
	}
	if !s.isSource(req.Source) {
		writeError(w, "That is not a file ARFABIT made.", nil)
		return
	}

	if req.Make == "read" {
		if len(req.Read) == 0 {
			writeError(w, "Tick the subtitles to read.", nil)
			return
		}
		var started, problems []string
		for _, stream := range req.Read {
			job, err := s.Runner.StartReading(context.Background(), pipeline.ReadingRequest{
				Original: req.Source, Title: req.Film, Stream: stream,
			})
			if err != nil {
				problems = append(problems, capitalise(err.Error())+".")
				continue
			}
			started = append(started, job.ID)
		}
		if len(started) == 0 {
			writeError(w, strings.Join(problems, " "), nil)
			return
		}
		writeJSON(w, map[string]any{"jobs": started, "problems": problems})
		return
	}

	job, err := s.Runner.StartProject(context.Background(), pipeline.ProjectRequest{
		Original:   req.Source,
		Film:       req.Film,
		Project:    req.Project,
		ClipsDir:   s.Config.Paths.Clips,
		LibraryDir: s.Config.Paths.Library,
	})
	if err != nil {
		writeError(w, capitalise(err.Error())+".", nil)
		return
	}
	writeJSON(w, map[string]any{"jobs": []string{job.ID}})
}

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/lab"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// PackageRequest is a Package to make from a Master.
type PackageRequest struct {
	Master string
	Film   string
	Year   int

	Package store.Package

	// ClipsDir and LibraryDir are where clips and whole masters go.
	ClipsDir   string
	LibraryDir string
}

// outputDirs are where packages put clips and films. They belong to the
// machine, not the job, so they are not part of the job record.
type outputDirs struct {
	clips, library string
}

func (r *Runner) configuredDirs() outputDirs {
	return outputDirs{clips: r.Config.Paths.Clips, library: r.Config.Paths.Library}
}

// Containers ARFABIT can make today. MP4 is to come.
var containers = map[string]string{"mkv": meta.VideoExt}

// checkPackage refuses a Package that could not be made, before anything
// waits in the line for it.
func checkPackage(p *store.Package) error {
	if len(p.Containers) == 0 {
		p.Containers = []string{"mkv"}
	}
	for _, c := range p.Containers {
		if _, ok := containers[c]; !ok {
			return fmt.Errorf("ARFABIT cannot make %s files yet", strings.ToUpper(c))
		}
	}

	if n := len(p.ItemsOf(store.KindVideo)); n != 1 {
		if n == 0 {
			return errors.New("a package needs a picture: add the video line")
		}
		return errors.New("a package can hold one picture")
	}

	for _, it := range p.Items {
		switch it.Action {
		case store.ActionCopy:
		case store.ActionConvert:
			if err := checkConversion(it); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q is not something a line item can do", it.Action)
		}
	}
	return nil
}

func checkConversion(it store.Item) error {
	switch it.Kind {
	case store.KindVideo:
		if it.To != "hevc" {
			return fmt.Errorf("a picture can only be converted to HEVC, not %q", it.To)
		}
		if !ffmpeg.Preset(it.Preset).Valid() {
			return fmt.Errorf("%q is not one of the speeds offered", it.Preset)
		}
	case store.KindAudio:
		if !slices.Contains([]string{"flac", "aac", "eac3"}, it.To) {
			return fmt.Errorf("sound can be converted to FLAC, AAC or E-AC-3, not %q", it.To)
		}
		// E-AC-3 is for surround a receiver can take whole. ffmpeg's encoder
		// stops at six channels and downmixes anything wider without a word
		// (§9), and for stereo AAC does the same job everywhere.
		if it.To == "eac3" {
			out := it.Channels
			if it.OutChannels > 0 {
				out = it.OutChannels
			}
			if out > 6 {
				return errors.New("E-AC-3 can only be made with up to six channels; choose AAC or FLAC for 7.1")
			}
			if out <= 2 {
				return errors.New("E-AC-3 is for surround; choose AAC for stereo")
			}
		}
		// Lossy sound converted to more than it had is a bigger file of
		// the same sound.
		if !it.Lossless && it.SourceBitrate > 0 && it.To != "flac" && bitsOf(it.Bitrate) > it.SourceBitrate {
			return fmt.Errorf("%s is more than the %d kbps the track has, which would only make the file bigger", it.Bitrate, it.SourceBitrate/1000)
		}
	case store.KindSubtitle:
		return errors.New("subtitles can only be copied until ARFABIT can read them into text")
	}
	return nil
}

// StartPackage makes a Package from a Master: one file per container, in the
// library for a whole film and in the lab for a stretch of one.
func (r *Runner) StartPackage(parent context.Context, req PackageRequest) (*Job, error) {
	if req.Master == "" {
		return nil, errors.New("there is no master to work from")
	}
	pkg := req.Package
	if err := checkPackage(&pkg); err != nil {
		return nil, err
	}

	film := req.Film
	if film == "" {
		film = filepath.Base(filepath.Dir(req.Master))
	}

	// A film goes into the library under its edition, so one already there
	// would be replaced. Refused now, before anything waits in the line.
	if pkg.WholeFilm() {
		title := meta.Title{Name: film, Year: req.Year}
		if err := wouldReplace(existingFilm(title.LibraryDir(req.LibraryDir), title, pkg.Edition)); err != nil {
			return nil, err
		}
	}

	rec := store.NewLabJob(store.NewJobID(time.Now(), film), film)
	rec.Kind = store.KindLab
	if pkg.WholeFilm() {
		rec.Kind = store.KindConvert
	}
	rec.Master = req.Master
	rec.Year = req.Year
	rec.Package = &pkg
	rec.Stage = store.StageQueued

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}
	log.Describe(rec.ID, film)

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel}
	job.Progress = Progress{Since: time.Now(), Operation: "Getting things ready"}

	r.begin(job)
	r.save(job)

	dirs := outputDirs{clips: req.ClipsDir, library: req.LibraryDir}
	go func() {
		defer cancel()
		defer r.finish(job)
		r.runPackage(ctx, job, dirs, r.Hold)
	}()

	return job, nil
}

// runPackage waits its turn at the processor, then makes the Package's files.
func (r *Runner) runPackage(ctx context.Context, job *Job, dirs outputDirs, hold time.Duration) {
	pkg := job.Package

	if r.Slots != nil {
		job.Stage = store.StageQueued
		r.save(job)

		err := r.Slots.Take(ctx, Ticket{
			ID:        job.ID,
			NotBefore: time.Now().Add(hold),
			Waiting: func() {
				job.Progress = Progress{Since: time.Now(), Operation: "Waiting for a turn"}
				job.Log.Printf(store.StageQueued, "Waiting to start: something else is using the processor.")
				r.save(job)
			},
		})
		if err != nil {
			r.stop(job, "Stopped while waiting to start.", "")
			return
		}
		defer r.Slots.Give()
	}

	job.Stage = store.StageLab
	if pkg.WholeFilm() {
		job.Stage = store.StagePackage
	}

	info, err := ffmpeg.Probe(ctx, job.Master)
	if err != nil {
		r.stop(job, "ARFABIT could not read the master.", err.Error())
		return
	}

	title := meta.Title{Name: job.Title, Year: job.Year}
	run := 0
	if !pkg.WholeFilm() {
		run = lab.NextRun(filepath.Join(dirs.clips, job.Title))
	}

	var made []lab.Clip
	for i, container := range pkg.Containers {
		if ctx.Err() != nil {
			r.stop(job, "Stopped at your request.", "")
			return
		}

		out := packageOutput(job, title, dirs, container, run)
		job.File = filepath.Base(out)
		job.Progress = Progress{
			Since:     time.Now(),
			Percent:   float64(i) / float64(len(pkg.Containers)) * 100,
			Operation: fmt.Sprintf("Making %s (%d of %d)", strings.ToUpper(container), i+1, len(pkg.Containers)),
		}
		r.save(job)

		start := time.Now()
		err := r.makePackageFile(ctx, job, info, out)

		var replace *ReplaceError
		switch {
		case errors.As(err, &replace):
			r.stop(job, fmt.Sprintf(
				"%s is already there, so ARFABIT stopped rather than replace it. Nothing was removed. Give this one a different edition, or move that file, and start it again.",
				filepath.Base(replace.Path)), replace.Path)
			return
		case ctx.Err() != nil:
			r.stop(job, "Stopped at your request.", "")
			return
		case err != nil:
			r.stop(job, "ARFABIT did not finish making the file.", detailOf(err))
			return
		}

		size := int64(0)
		if st, err := os.Stat(out); err == nil {
			size = st.Size()
		}
		job.Log.Printf(job.Stage, "Made %s (%s) in %s.", filepath.Base(out), HumanBytes(size), time.Since(start).Round(time.Second))
		made = append(made, lab.Clip{Name: packageName(pkg), Path: out, Size: size, Took: time.Since(start)})
		job.Made = append(job.Made, out)

		if pkg.WholeFilm() {
			job.Delivery = out
			job.Stage = store.StageDeliver
			if err := r.deliver(job, title, pkg.Edition); err != nil {
				r.stop(job, "ARFABIT made the file but could not add it to your library's list.", err.Error())
				return
			}
		}
	}

	// A clip is made to judge, and says what the whole film would come to.
	if !pkg.WholeFilm() {
		job.Comparison = lab.Compare(made, pkg.Length, time.Duration(info.Duration*float64(time.Second)))
	}

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%s is ready.", job.File)
	if len(made) > 1 {
		job.Note = fmt.Sprintf("%d files are ready.", len(made))
	}
	job.Log.Printf(job.Stage, "%s", job.Note)
	r.save(job)
}

// packageName is what a package is called in a clip's edition: its edition,
// or the blueprint it came from, or simply what it is.
func packageName(p *store.Package) string {
	for _, name := range []string{p.Edition, p.Blueprint} {
		if name != "" {
			return name
		}
	}
	return "Package"
}

// packageOutput is where one of a Package's files goes, and what it is called.
func packageOutput(job *Job, title meta.Title, dirs outputDirs, container string, run int) string {
	pkg := job.Package
	ext := containers[container]
	if pkg.WholeFilm() {
		name := strings.TrimSuffix(title.VideoName(pkg.Edition), meta.VideoExt) + ext
		return filepath.Join(title.LibraryDir(dirs.library), name)
	}
	name := strings.TrimSuffix(lab.ClipName(job.Title, run, packageName(pkg), pkg.At), meta.VideoExt) + ext
	return filepath.Join(dirs.clips, job.Title, name)
}

// makePackageFile makes one file from the Package's line items.
//
// A stretch of the Master is cut first, into a piece with every stream it
// needs copied as it is, and the file is made from the piece with no seeking
// at all. Seeking while copying some streams and converting others moved them
// against each other: copied sound started at the keyframe before the cut, up
// to 0.7 seconds ahead of the picture. Copied together, they keep their true
// timing. The piece is ARFABIT's own, made for this and removed after.
//
// Copied streams can only start on a keyframe, so a clip starts at the last
// one before the time asked for: on a disc, usually under a second before.
func (r *Runner) makePackageFile(ctx context.Context, job *Job, info *ffmpeg.MediaInfo, out string) error {
	pkg := job.Package
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := refuseToReplace(out); err != nil {
		return err
	}

	input := job.Master
	duration := time.Duration(info.Duration * float64(time.Second))

	// Where each Master stream is in the file being read.
	index := func(source int) int { return source }

	if !pkg.WholeFilm() {
		var sources []int
		for _, it := range pkg.Items {
			if !slices.Contains(sources, it.Source) {
				sources = append(sources, it.Source)
			}
		}

		piece := filepath.Join(filepath.Dir(out), ".arfabit-piece-"+job.ID+".mkv")
		args := []string{"-hide_banner", "-y",
			"-ss", fmt.Sprintf("%.3f", pkg.At.Seconds()), "-i", job.Master,
			"-t", fmt.Sprintf("%.3f", pkg.Length.Seconds())}
		for _, s := range sources {
			args = append(args, "-map", fmt.Sprintf("0:%d", s))
		}
		args = append(args, "-c", "copy", "-map_chapters", "-1", piece)

		job.Log.Printf(job.Stage, "Cutting %s from %s.", formatDuration(pkg.Length), formatDuration(pkg.At))
		if err := ffmpeg.Run(ctx, args, ffmpeg.RunOptions{Duration: pkg.Length}); err != nil {
			_ = os.Remove(piece)
			return err
		}
		defer os.Remove(piece)

		input = piece
		duration = pkg.Length
		index = func(source int) int { return slices.Index(sources, source) }
	}

	req, err := encodeRequest(pkg, info, input, out, index)
	if err != nil {
		return err
	}
	args, err := req.Args()
	if err != nil {
		return err
	}

	start := time.Now()
	var writeRate rateTracker
	err = ffmpeg.Run(ctx, args, ffmpeg.RunOptions{
		Duration: duration,
		OnProgress: func(p ffmpeg.Progress) {
			job.Progress = Progress{
				Percent:   p.Percent(),
				Operation: job.Progress.Operation,
				Remaining: humanDuration(p.Remaining().Round(time.Minute)),
				Since:     job.Progress.Since,
				Rate:      HumanRate(writeRate.Observe(p.Bytes)),
			}
			r.notify(job)
		},
	})
	if err != nil {
		return err
	}

	// A whole film's encode is what the estimator learns from.
	video := pkg.ItemsOf(store.KindVideo)[0]
	if pkg.WholeFilm() && video.Action == store.ActionConvert {
		if written, err := os.Stat(out); err == nil {
			if v := info.VideoStream(); v != nil {
				r.Calibration.ObserveEncode(calibrationPlan(pkg, info), v.Width, v.Height,
					written.Size(), duration, time.Since(start))
			}
		}
	}
	return nil
}

// encodeRequest turns the line items into what the encoder is asked for.
// index finds a Master stream in the file being read.
func encodeRequest(pkg *store.Package, info *ffmpeg.MediaInfo, input, out string, index func(int) int) (ffmpeg.EncodeRequest, error) {
	req := ffmpeg.EncodeRequest{Input: input, Output: out, Chapters: pkg.WholeFilm()}

	video := pkg.ItemsOf(store.KindVideo)[0]
	source := streamAt(info, video.Source)
	if source == nil || source.Kind != "video" {
		return req, fmt.Errorf("the master has no picture at stream %d", video.Source)
	}
	req.VideoSourceIndex = index(video.Source)
	if video.Action == store.ActionCopy {
		req.Video = ffmpeg.VideoPlan{Copy: true}
	} else {
		req.Video = ffmpeg.VideoPlan{CRF: video.CRF, Preset: ffmpeg.Preset(video.Preset)}
		// The HDR metadata is the Master's, whatever is being read (§9).
		req.HDR = source.HDR
		req.Color = source.ColorInfo
	}

	for i, it := range pkg.ItemsOf(store.KindAudio) {
		if s := streamAt(info, it.Source); s == nil || s.Kind != "audio" {
			return req, fmt.Errorf("the master has no sound at stream %d", it.Source)
		}
		track := ffmpeg.AudioTrack{
			SourceIndex: index(it.Source),
			Copy:        it.Action == store.ActionCopy,
			Codec:       it.To,
			Bitrate:     it.Bitrate,
			Channels:    it.OutChannels,
			Lang:        it.Lang,
			Title:       itemTitle(it),
			Default:     i == 0,
		}
		// Lossless sound has no bitrate to choose.
		if it.To == "flac" {
			track.Bitrate = ""
		}
		req.Audio = append(req.Audio, track)
	}

	// No subtitle track is switched on by default: a player showing
	// subtitles nobody asked for is worse than one that needs a click.
	for _, it := range pkg.ItemsOf(store.KindSubtitle) {
		if s := streamAt(info, it.Source); s == nil || s.Kind != "subtitle" {
			return req, fmt.Errorf("the master has no subtitles at stream %d", it.Source)
		}
		req.SubtitleCopies = append(req.SubtitleCopies, ffmpeg.SubtitleCopy{
			SourceIndex: index(it.Source),
			Lang:        it.Lang,
			Title:       languageName(it.Lang),
		})
	}

	return req, nil
}

// itemTitle is the name a player shows for a sound track: its width, and
// whether it is lossless, in the words a menu would use.
func itemTitle(it store.Item) string {
	channels := it.Channels
	if it.OutChannels > 0 {
		channels = it.OutChannels
	}
	name := layoutName(channels, "")
	lossless := it.Lossless && it.Action == store.ActionCopy
	if it.Action == store.ActionConvert {
		lossless = it.To == "flac" && it.Lossless
	}
	if lossless {
		name += " lossless"
	}
	return name
}

func streamAt(info *ffmpeg.MediaInfo, index int) *ffmpeg.Stream {
	for i := range info.Streams {
		if info.Streams[i].Index == index {
			return &info.Streams[i]
		}
	}
	return nil
}

// calibrationPlan describes a Package the way the estimator records encodes:
// by picture settings, with the sound's size left out of what it learns.
func calibrationPlan(pkg *store.Package, info *ffmpeg.MediaInfo) *store.Plan {
	video := pkg.ItemsOf(store.KindVideo)[0]
	plan := &store.Plan{CRF: video.CRF, Preset: video.Preset}
	for _, it := range pkg.ItemsOf(store.KindAudio) {
		plan.Audio = append(plan.Audio, store.PlannedAudio{Selected: true, Copy: it.Action == store.ActionCopy})
	}
	return plan
}

// bindToMaster points a package planned from a disc's scan at the Master's own
// tracks, which MakeMKV numbers its own way.
//
// A track is found by what it is, in order: the same kind, language, format
// and width, not yet taken. MakeMKV turns a disc's uncompressed sound into
// FLAC, so failing an exact match, the same kind, language and width will do.
// Lines made from one track — kept, and converted — stay on one track.
func bindToMaster(pkg *store.Package, master []Track) error {
	found := map[string]int{}
	used := map[int]bool{}

	for i := range pkg.Items {
		it := &pkg.Items[i]
		key := fmt.Sprintf("%s:%d", it.Kind, it.Source)
		if m, ok := found[key]; ok {
			it.Source = m
			continue
		}

		m := matchTrack(*it, master, used, true)
		if m == nil {
			m = matchTrack(*it, master, used, false)
		}
		if m == nil {
			return fmt.Errorf("the master has no %s track", strings.ToLower(it.Label))
		}
		used[m.Index] = true
		found[key] = m.Index
		it.Source, it.Codec = m.Index, m.Codec
	}
	return nil
}

func matchTrack(it store.Item, master []Track, used map[int]bool, exact bool) *Track {
	for i := range master {
		m := &master[i]
		if m.Kind != it.Kind || used[m.Index] {
			continue
		}
		if it.Kind == store.KindVideo {
			return m
		}
		if !strings.EqualFold(m.Lang, it.Lang) {
			continue
		}
		if it.Kind == store.KindAudio && m.Channels != it.Channels {
			continue
		}
		if exact && m.Codec != it.Codec {
			continue
		}
		return m
	}
	return nil
}

// sentence makes a sentence of a message written as a fragment.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

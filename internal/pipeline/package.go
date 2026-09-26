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
	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
)

// ProjectRequest is a Project to make from a file ARFABIT made: an Original,
// or anything made from one.
type ProjectRequest struct {
	Original string
	Film     string
	Year     int

	Project store.Project

	// LibraryDir is where what is made goes, in the film's own folder.
	LibraryDir string
}

// Containers ARFABIT can make. MKV is what §4 tested; MP4 is made when asked
// for, and nothing about how it plays is assumed.
var containers = map[string]string{"mkv": meta.VideoExt, "mp4": ".mp4"}

// checkProject refuses a Project that could not be made, before anything
// waits in the line for it. canRead says whether this computer can read
// subtitles into text (§10).
//
// A Project with video or audio makes one file per container, with its
// subtitles copied inside and those read into text beside it as SRT. One with
// subtitles alone makes a file for each: SRT for text, and for picture
// subtitles copied as they are, SUP, the pictures as a Blu-ray holds them.
func checkProject(p *store.Project, canRead bool) error {
	if len(p.Containers) == 0 {
		p.Containers = []string{"mkv"}
	}
	for _, c := range p.Containers {
		if _, ok := containers[c]; !ok {
			return fmt.Errorf("ARFABIT cannot make %s files yet", strings.ToUpper(c))
		}
	}
	if len(p.Items) == 0 {
		return errors.New("there is nothing to make: add video, audio or subtitles from the file")
	}
	if len(p.ItemsOf(store.KindVideo)) > 1 {
		return errors.New("a file can hold one video")
	}

	// The original is filed under this edition, beside what is made from it
	// (§6). Subtitles read from the whole of it may go there too.
	if strings.EqualFold(strings.TrimSpace(p.Edition), meta.OriginalEdition) && (p.HasAV() || !p.Whole()) {
		return fmt.Errorf("%q is the edition the original is kept under; give this another", meta.OriginalEdition)
	}

	if p.HasAV() && slices.Contains(p.Containers, "mp4") {
		if err := checkMP4(p); err != nil {
			return err
		}
	}

	for _, it := range p.Items {
		switch it.Action {
		case store.ActionCopy:
			if it.Kind == store.KindSubtitle && !p.HasAV() && it.Codec != pictureSubtitles && it.Codec != "subrip" {
				return errors.New("on their own, only a Blu-ray's picture subtitles and text subtitles can be taken out; add video or audio to keep these in a file")
			}
		case store.ActionConvert:
			if err := checkConversion(it, canRead); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q is not something a line item can do", it.Action)
		}
	}

	// Each subtitle file is named by its language (§6), so two of one kind
	// in one language would need the same name.
	seen := map[string]bool{}
	for _, path := range subtitleFiles(p, "x") {
		if seen[path] {
			return fmt.Errorf("two subtitle files would both be %s; keep one track of each kind per language", strings.TrimPrefix(path, "x"))
		}
		seen[path] = true
	}
	return nil
}

// subtitleFiles are the files a Project's subtitle line items become beside
// stem, the name of what it makes without its extension, by each line item's
// place in Items: the stem, the track's language, and what it is, which is how
// Plex matches a subtitle file to a video (§6).
func subtitleFiles(p *store.Project, stem string) map[int]string {
	files := map[int]string{}
	for i, it := range p.Items {
		if it.Kind != store.KindSubtitle {
			continue
		}
		ext := ""
		switch {
		case it.Action == store.ActionConvert:
			ext = ".srt"
		case p.HasAV():
			// Copied into the file itself.
		case it.Codec == pictureSubtitles:
			ext = ".sup"
		case it.Codec == "subrip":
			ext = ".srt"
		}
		if ext != "" {
			files[i] = stem + "." + ocr.Tag(it.Lang) + ext
		}
	}
	return files
}

// outputStem is the name of what a Project makes, in the film's folder,
// without its extension.
func outputStem(library string, title meta.Title, pkg *store.Project) string {
	return filepath.Join(title.LibraryDir(library), title.BaseName(pkg.Edition))
}

// outputs lists every file a Project makes.
func outputs(library string, title meta.Title, pkg *store.Project) []string {
	stem := outputStem(library, title, pkg)
	var paths []string
	if pkg.HasAV() {
		for _, c := range pkg.Containers {
			paths = append(paths, stem+containers[c])
		}
	}
	for i := range pkg.Items {
		if path, ok := subtitleFiles(pkg, stem)[i]; ok {
			paths = append(paths, path)
		}
	}
	return paths
}

// AlreadyThere lists the files a Project from a file would make that are
// already in the library, which it will not replace, so the page can say so
// before Start.
func AlreadyThere(library, film string, pkg store.Project) []string {
	if len(pkg.Containers) == 0 {
		pkg.Containers = []string{"mkv"}
	}
	return alreadyThere(library, meta.Title{Name: film}, &pkg)
}

// Outputs lists the files a Project from a file would make, so the page can
// show their names as it is set up.
func Outputs(library, film string, pkg store.Project) []string {
	if len(pkg.Containers) == 0 {
		pkg.Containers = []string{"mkv"}
	}
	return outputs(library, meta.Title{Name: film}, &pkg)
}

// alreadyThere lists what a Project would make that is already there, which
// it will not replace (§0.6). A video under its edition counts as MKV or MP4,
// since to Plex both are that edition.
func alreadyThere(library string, title meta.Title, pkg *store.Project) []string {
	var found []string
	if pkg.HasAV() {
		found = existingFilm(title.LibraryDir(library), title, pkg.Edition)
	}
	for _, path := range outputs(library, title, pkg) {
		if strings.HasSuffix(path, ".srt") || strings.HasSuffix(path, ".sup") {
			found = append(found, existingAt(path)...)
		}
	}
	return found
}

// checkMP4 refuses line items an MP4 cannot hold as they are, found with
// ffmpeg 9.0.2: picture subtitles, which MP4 has no place for, and Dolby
// TrueHD, which ffmpeg calls experimental in MP4 and will not write without
// being told to. Text subtitles copied go in as MP4's own text format.
func checkMP4(p *store.Project) error {
	for _, it := range p.Items {
		if it.Action != store.ActionCopy {
			continue
		}
		switch {
		case it.Kind == store.KindSubtitle && (it.Codec == pictureSubtitles || it.Codec == "dvd_subtitle"):
			return errors.New("an MP4 cannot hold picture subtitles; read them into text, leave them out, or make an MKV")
		case it.Kind == store.KindAudio && it.Codec == "truehd":
			return errors.New("ffmpeg calls Dolby TrueHD in an MP4 experimental, so ARFABIT does not put it there as it is; convert it, or make an MKV")
		}
	}
	return nil
}

func checkConversion(it store.Item, canRead bool) error {
	switch it.Kind {
	case store.KindVideo:
		if it.To != "hevc" {
			return fmt.Errorf("video can only be converted to HEVC, not %q", it.To)
		}
		if !ffmpeg.Preset(it.Preset).Valid() {
			return fmt.Errorf("%q is not one of the speeds offered", it.Preset)
		}
	case store.KindAudio:
		if !slices.Contains([]string{"flac", "aac", "eac3"}, it.To) {
			return fmt.Errorf("audio can be converted to FLAC, AAC or E-AC-3, not %q", it.To)
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
		if !canRead {
			return errors.New("subtitles cannot be read into text on this computer, so they can only be copied as they are")
		}
		if it.Codec != pictureSubtitles {
			return errors.New("only a Blu-ray's picture subtitles can be read into text")
		}
		if it.To != "srt" {
			return fmt.Errorf("subtitles can only be converted to text (SRT), not %q", it.To)
		}
	}
	return nil
}

// StartProject makes a Project from a file ARFABIT made, into the film's
// folder in the library: all of it, or a stretch.
func (r *Runner) StartProject(parent context.Context, req ProjectRequest) (*Job, error) {
	if req.Original == "" {
		return nil, errors.New("there is no file to work from")
	}
	pkg := req.Project
	if err := checkProject(&pkg, r.OCR != nil); err != nil {
		return nil, err
	}

	film := req.Film
	if film == "" {
		film = filepath.Base(filepath.Dir(req.Original))
	}

	// Refused now, before anything waits in the line, if it would replace
	// something already there.
	title := meta.Title{Name: film, Year: req.Year}
	if err := wouldReplace(alreadyThere(req.LibraryDir, title, &pkg)); err != nil {
		return nil, err
	}

	rec := store.NewLabJob(store.NewJobID(time.Now(), film), film)
	rec.Kind = store.KindLab
	if pkg.Whole() {
		rec.Kind = store.KindConvert
	}
	rec.Original = req.Original
	rec.Year = req.Year
	rec.Project = &pkg
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

	// Subtitles converted to text from the whole of it are read from the
	// source now, each as a task of its own, if they have not been already.
	r.readMissing(parent, req.Original, film, req.Year, &pkg)

	go func() {
		defer cancel()
		defer r.finish(job)
		r.runPackage(ctx, job, req.LibraryDir, r.Hold)
	}()

	return job, nil
}

// runPackage makes a Project's files: waiting its turn at the processor if it
// makes video or audio, then the files themselves, then its subtitle files.
func (r *Runner) runPackage(ctx context.Context, job *Job, library string, hold time.Duration) {
	r.mu.Lock()
	av := job.Project.HasAV()
	r.mu.Unlock()

	// Subtitles alone take little processor, so they do not wait for it.
	if r.Slots != nil && av {
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

	// From here the line items are what they are: one planned with its disc
	// could be changed until now (editing.go).
	r.mu.Lock()
	pkg := job.Project
	job.Stage = store.StageLab
	if pkg.Whole() {
		job.Stage = store.StagePackage
	}
	r.mu.Unlock()

	info, err := ffmpeg.Probe(ctx, job.Original)
	if err != nil {
		r.stop(job, "ARFABIT could not read "+filepath.Base(job.Original)+".", err.Error())
		return
	}

	notes, err := r.makeProject(ctx, job, info, library)

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

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%s is ready.", job.File)
	if len(job.Made) > 1 {
		job.Note = fmt.Sprintf("%d files are ready.", len(job.Made))
	}
	for _, note := range notes {
		job.Note += " " + note
	}
	job.Log.Printf(job.Stage, "%s", job.Note)
	r.save(job)
}

// packageName is what a Project was made as, for comparing parts: its
// edition, or the blueprint it came from, or simply what it is.
func packageName(p *store.Project) string {
	for _, name := range []string{p.Edition, p.Blueprint} {
		if name != "" {
			return name
		}
	}
	return "Project"
}

// makeProject makes a Project's files beside stem, and returns anything to add
// to the job's note.
//
// A stretch of the source is cut first, into a piece with every stream it
// needs copied as it is, and everything is made from the piece with no seeking
// at all. Seeking while copying some streams and converting others moved them
// against each other: copied audio started at the keyframe before the cut, up
// to 0.7 seconds ahead of the video. Copied together, they keep their true
// timing. The piece is ARFABIT's own, made for this and removed after. Copied
// streams can only start on a keyframe, so a stretch starts at the last one
// before the time asked for: on a disc, usually under a second before.
func (r *Runner) makeProject(ctx context.Context, job *Job, info *ffmpeg.MediaInfo, library string) ([]string, error) {
	pkg := job.Project
	title := meta.Title{Name: job.Title, Year: job.Year}
	stem := outputStem(library, title, pkg)
	if err := os.MkdirAll(filepath.Dir(stem), 0o755); err != nil {
		return nil, err
	}
	// Looked for again, since a job can wait in the line for hours.
	if found := alreadyThere(library, title, pkg); len(found) > 0 {
		return nil, &ReplaceError{Path: found[0]}
	}

	input := job.Original
	duration := time.Duration(info.Duration * float64(time.Second))
	index := func(source int) int { return source } // where each stream is in input

	if !pkg.Whole() {
		var sources []int
		for _, it := range pkg.Items {
			if !slices.Contains(sources, it.Source) {
				sources = append(sources, it.Source)
			}
		}
		piece := filepath.Join(filepath.Dir(stem), ".arfabit-piece-"+job.ID+".mkv")
		args := []string{"-hide_banner", "-y",
			"-ss", fmt.Sprintf("%.3f", pkg.At.Seconds()), "-i", job.Original,
			"-t", fmt.Sprintf("%.3f", pkg.Length.Seconds())}
		for _, s := range sources {
			args = append(args, "-map", fmt.Sprintf("0:%d", s))
		}
		args = append(args, "-c", "copy", "-map_chapters", "-1", piece)

		job.Log.Printf(job.Stage, "Cutting %s from %s.", formatDuration(pkg.Length), formatDuration(pkg.At))
		if err := ffmpeg.Run(ctx, args, ffmpeg.RunOptions{Duration: pkg.Length}); err != nil {
			_ = os.Remove(piece)
			return nil, err
		}
		defer os.Remove(piece)

		input = piece
		duration = pkg.Length
		index = func(source int) int { return slices.Index(sources, source) }
	}

	var (
		notes []string
		made  []lab.Clip
	)
	if pkg.HasAV() {
		for i, container := range pkg.Containers {
			out := stem + containers[container]
			job.File = filepath.Base(out)
			job.Progress = Progress{
				Since:     time.Now(),
				Percent:   float64(i) / float64(len(pkg.Containers)) * 100,
				Operation: fmt.Sprintf("Making %s (%d of %d)", strings.ToUpper(container), i+1, len(pkg.Containers)),
			}
			r.save(job)

			took, err := r.encodeFile(ctx, job, info, input, index, duration, out)
			if err != nil {
				return nil, err
			}
			size := int64(0)
			if st, err := os.Stat(out); err == nil {
				size = st.Size()
			}
			made = append(made, lab.Clip{Name: packageName(pkg), Path: out, Size: size, Took: took})
			job.Made = append(job.Made, out)
			if pkg.Whole() {
				job.Delivery = out
			}
		}
	}

	// Subtitle files: the whole of a source's read ones are copies of the
	// SRT beside it, taken at DELIVER; a stretch's are read from its piece.
	if pkg.Whole() {
		job.Stage = store.StageDeliver
		r.save(job)
	}
	if note, err := r.subtitleFilesOf(ctx, job, info, input, index, stem); err != nil {
		return nil, err
	} else if note != "" {
		notes = append(notes, note)
	}
	if job.File == "" && len(job.Made) > 0 {
		job.File = filepath.Base(job.Made[0])
	}

	if pkg.Whole() && pkg.HasAV() {
		if err := r.deliver(job, title, pkg.Edition); err != nil {
			notes = append(notes, "ARFABIT could not add it to your library's list: "+err.Error())
		}
	}
	// A stretch is made to judge, and says what the whole would come to.
	if !pkg.Whole() && pkg.HasAV() {
		job.Comparison = lab.Compare(made, pkg.Length, time.Duration(info.Duration*float64(time.Second)))
	}
	return notes, nil
}

// encodeFile makes one video or audio file from the Project's line items,
// reading input, and returns how long it took.
func (r *Runner) encodeFile(ctx context.Context, job *Job, info *ffmpeg.MediaInfo, input string, index func(int) int, duration time.Duration, out string) (time.Duration, error) {
	pkg := job.Project
	req, err := encodeRequest(pkg, info, input, out, index)
	if err != nil {
		return 0, err
	}
	req.MP4 = strings.EqualFold(filepath.Ext(out), ".mp4")
	args, err := req.Args()
	if err != nil {
		return 0, err
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
		return 0, err
	}
	took := time.Since(start)

	size := int64(0)
	if st, err := os.Stat(out); err == nil {
		size = st.Size()
	}
	job.Log.Printf(job.Stage, "Made %s (%s) in %s.", filepath.Base(out), HumanBytes(size), took.Round(time.Second))

	// A whole encode is what the estimator learns from.
	if videos := pkg.ItemsOf(store.KindVideo); pkg.Whole() && len(videos) == 1 && videos[0].Action == store.ActionConvert && size > 0 {
		if v := info.VideoStream(); v != nil {
			r.Calibration.ObserveEncode(calibrationPlan(pkg, info), v.Width, v.Height, size, duration, took)
		}
	}
	return took, nil
}

// encodeRequest turns the line items into what the encoder is asked for.
// index finds an Original stream in the file being read.
func encodeRequest(pkg *store.Project, info *ffmpeg.MediaInfo, input, out string, index func(int) int) (ffmpeg.EncodeRequest, error) {
	req := ffmpeg.EncodeRequest{Input: input, Output: out, Chapters: pkg.Whole()}

	videos := pkg.ItemsOf(store.KindVideo)
	req.NoVideo = len(videos) == 0
	if !req.NoVideo {
		video := videos[0]
		source := streamAt(info, video.Source)
		if source == nil || source.Kind != "video" {
			return req, fmt.Errorf("the source has no video at stream %d", video.Source)
		}
		req.VideoSourceIndex = index(video.Source)
		req.HEVC = video.Action == store.ActionConvert || source.Codec == "hevc"
		if video.Action == store.ActionCopy {
			req.Video = ffmpeg.VideoPlan{Copy: true}
		} else {
			req.Video = ffmpeg.VideoPlan{CRF: video.CRF, Preset: ffmpeg.Preset(video.Preset)}
			// The HDR metadata is the source's, whatever is being read (§9).
			req.HDR = source.HDR
			req.Color = source.ColorInfo
		}
	}

	for i, it := range pkg.ItemsOf(store.KindAudio) {
		if s := streamAt(info, it.Source); s == nil || s.Kind != "audio" {
			return req, fmt.Errorf("the source has no audio at stream %d", it.Source)
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

	for _, it := range pkg.ItemsOf(store.KindSubtitle) {
		if s := streamAt(info, it.Source); s == nil || s.Kind != "subtitle" {
			return req, fmt.Errorf("the source has no subtitles at stream %d", it.Source)
		}
		// Converted, they become SRT files beside the one made.
		if it.Action == store.ActionConvert {
			continue
		}
		// No subtitle track is switched on by default: a player showing
		// subtitles nobody asked for is worse than one that needs a click.
		req.Subtitles = append(req.Subtitles, ffmpeg.SubtitleTrack{
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

// calibrationPlan describes a Project the way the estimator records encodes:
// by video settings, with the sound's size left out of what it learns.
func calibrationPlan(pkg *store.Project, info *ffmpeg.MediaInfo) *store.Plan {
	plan := &store.Plan{}
	if videos := pkg.ItemsOf(store.KindVideo); len(videos) > 0 {
		plan.CRF, plan.Preset = videos[0].CRF, videos[0].Preset
	}
	for _, it := range pkg.ItemsOf(store.KindAudio) {
		plan.Audio = append(plan.Audio, store.PlannedAudio{Selected: true, Copy: it.Action == store.ActionCopy})
	}
	return plan
}

// bindToOriginal points a package planned from a disc's scan at the Original's own
// tracks, which MakeMKV numbers its own way.
//
// A track is found by what it is, in order: the same kind, language, format
// and width, not yet taken. MakeMKV turns a disc's uncompressed sound into
// FLAC, so failing an exact match, the same kind, language and width will do.
// Lines made from one track — kept, and converted — stay on one track.
//
// Subtitle tracks are found by their place among the disc's (bindSubtitles),
// since a disc often has several in one language and format.
func bindToOriginal(pkg *store.Project, disc, tracks []Track) error {
	found := map[string]int{}
	used := map[int]bool{}
	subtitles := bindSubtitles(disc, tracks)

	for i := range pkg.Items {
		it := &pkg.Items[i]
		key := fmt.Sprintf("%s:%d", it.Kind, it.Source)
		if m, ok := found[key]; ok {
			it.Source = m
			continue
		}
		if m, ok := subtitles[it.Source]; ok && it.Kind == store.KindSubtitle && !used[m] {
			used[m] = true
			found[key] = m
			for _, t := range tracks {
				if t.Index == m {
					it.Source, it.Codec = m, t.Codec
				}
			}
			continue
		}

		m := matchTrack(*it, tracks, used, true)
		if m == nil {
			m = matchTrack(*it, tracks, used, false)
		}
		if m == nil {
			return fmt.Errorf("the original has no %s track", strings.ToLower(it.Label))
		}
		used[m.Index] = true
		found[key] = m.Index
		it.Source, it.Codec = m.Index, m.Codec
	}
	return nil
}

func matchTrack(it store.Item, tracks []Track, used map[int]bool, exact bool) *Track {
	for i := range tracks {
		m := &tracks[i]
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

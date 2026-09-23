package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/eject"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// Runner carries one disc through the pipeline.
type Runner struct {
	Config      config.Config
	Store       *store.Store
	Backend     *makemkv.Backend
	Calibration *Calibration

	// Index is the offline film list, used to confirm a title and find its
	// year. Nil when it has not been downloaded, in which case the disc's own
	// name is used.
	Index *meta.Index

	// OnUpdate is called whenever the job changes, so the web view can
	// refresh without polling.
	OnUpdate func(*Job)

	// OnLog is called for every line written to a job's log, as it is
	// written. The page shows progress from these, so a stage that takes
	// minutes is never silent.
	OnLog func(Entry)

	mu      sync.Mutex
	current *Job
}

// Job is a job record plus the live state the UI needs.
type Job struct {
	*store.Job

	Log      *Log     `json:"-"`
	Progress Progress `json:"progress"`
	Space    Space    `json:"space"`
	cancel   context.CancelFunc
}

// Progress is how far the current stage has got.
type Progress struct {
	Percent   float64 `json:"percent"`
	Operation string  `json:"operation"`
	Remaining string  `json:"remaining"`

	// Since is when this stage began, so the page can show a clock that
	// ticks. A moving number is the difference between a program that is
	// working and one that appears to have died.
	Since time.Time `json:"since"`

	// Expected is how long the stage was estimated to take, for context
	// beside the clock.
	Expected string `json:"expected,omitempty"`
}

// Current returns the job in flight, or nil.
func (r *Runner) Current() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Scan reads the disc and builds a Plan, stopping short of doing anything to
// it. Nothing starts until the user says so (§8).
func (r *Runner) Scan(ctx context.Context, drive disc.Drive) (*Job, error) {
	// One job at a time, for now.
	//
	// Replacing a running job would leave it running with its progress
	// invisible and Stop pointing at the wrong thing — an encode quietly
	// orphaned mid-film. Refusing is not the eventual answer, since the drive
	// is free once the disc is out and a second disc could perfectly well be
	// read while the first is still encoding, but losing track of a job is
	// worse than waiting.
	if busy := r.Current(); busy != nil && busy.State == store.StateRunning {
		return nil, fmt.Errorf(
			"%s is still being worked on (%s). ARFABIT can only manage one disc at a time for now",
			busy.Title, strings.ToLower(stageWords(busy.Stage)))
	}

	// A scan that was never started is finished with rather than left hanging
	// as though it were still expecting an answer. Reading a disc again is
	// how somebody says they have changed their mind.
	r.retirePreviousScan()

	rec := store.NewJob(store.NewJobID(time.Now(), drive.Label))
	rec.DiscLabel = drive.Label
	rec.Drive = drive.Device
	rec.Stage = store.StageScan

	logPath := r.Store.LogPath(rec.ID)

	// Every line goes out as it is written. Without this the page shows
	// nothing at all until the stage ends, and a Blu-ray scan takes minutes.
	log, err := NewLog(logPath, func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}

	// The scan is cancellable from the start. Without this, Stop had nothing
	// to cancel during a scan and said it was stopping while the drive ground
	// on for minutes.
	ctx, cancel := context.WithCancel(ctx)
	job := &Job{Job: rec, Log: log, cancel: cancel}
	r.setCurrent(job)

	// Tell the page a disc is being read before doing it, not after.
	r.notify(job)

	job.Progress = Progress{Since: time.Now(), Operation: "Reading the disc"}
	log.Printf(store.StageScan, "Reading the disc in %s. This takes a minute or two.", drive.Name)

	// MakeMKV talks while it works; passing that through is the difference
	// between a page that looks busy and one that looks broken.
	//
	// The scan waits its turn if the drive is momentarily busy rather than
	// giving up: the poll that watches for a disc holds it for a fraction of
	// a second, and refusing over that would be maddening.
	d, err := r.Backend.ScanWithMessages(ctx, drive.Index, func(m makemkv.Message) {
		log.Printf(store.StageScan, "%s", m.Text)
	})
	if err != nil {
		if ctx.Err() != nil {
			r.stop(job, "Stopped at your request.", "")
			return job, ctx.Err()
		}
		r.stop(job, readFailureNote(err), detailOf(err))
		return job, err
	}

	rec.DiscName = d.Name
	rec.DiscKind = string(d.Kind)
	if rec.DiscLabel == "" {
		rec.DiscLabel = d.Label
	}

	sel := disc.SelectTitles(d.Titles)
	if sel.Feature < 0 {
		r.stop(job, sel.Reason, "")
		return job, fmt.Errorf("no usable title")
	}

	plan, err := BuildPlan(d, sel, r.Config.Profile)
	if err != nil {
		r.stop(job, "ARFABIT could not work out what to do with this disc.", err.Error())
		return job, err
	}

	title := d.Titles[sel.Feature]
	rec.Plan = plan
	rec.Stage = store.StagePlan
	rec.State = store.StateWaiting
	rec.Title = titleFrom(d)

	// The offline list confirms the name and supplies the year, which no disc
	// label carries. The best match is offered, never applied silently: the
	// Plan shows the candidates and the user decides (§11).
	if matches := r.Index.Lookup(bestLabel(d), int(title.Duration.Minutes()), 3); len(matches) > 0 {
		rec.Matches = matches
		best := matches[0]
		rec.Title = best.Title.Name
		rec.Year = best.Title.Year
		log.Printf(store.StagePlan, "This looks like %s — %s.", best.Title, best.Why)
	} else if r.Index != nil {
		log.Printf(store.StagePlan, "This disc is not in the film list, so its own name is used.")
	}

	// Estimates are shown before anything starts, so the user knows what they
	// are agreeing to.
	ripEst := r.Calibration.EstimateRip(drive.Device, d.Kind, title.SizeBytes)
	packEst := r.Calibration.EstimatePackage(plan, title.Duration)
	plan.EstimatedSize = packEst.Size
	plan.EstimatedTime = ripEst.Time + packEst.Time

	// The master and the delivery both exist at once, so both must fit.
	job.Space, _ = CheckSpace(title.SizeBytes+packEst.Size, r.Config.Paths.Masters, r.Config.Paths.Library)

	log.Printf(store.StagePlan, "Found %s, %s, %s.", rec.Title, plan.Duration, HumanBytes(title.SizeBytes))
	log.Printf(store.StagePlan, "%s", sel.Reason)
	if plan.Obfuscated {
		log.Printf(store.StagePlan, "This disc hides its main feature among identical-looking titles.")
	}
	if msg := job.Space.Describe(); msg != "" {
		log.Printf(store.StagePlan, "%s", msg)
	}

	r.save(job)
	return job, nil
}

// Start runs the rest of the pipeline for a scanned job.
func (r *Runner) Start(parent context.Context) error {
	job := r.Current()
	if job == nil {
		return errors.New("there is no disc waiting")
	}
	if job.State == store.StateRunning && job.Stage != store.StagePlan {
		return errors.New("this disc is already being worked on")
	}
	if !job.Space.Fits {
		return errors.New(job.Space.Describe())
	}

	ctx, cancel := context.WithCancel(parent)
	job.cancel = cancel
	job.State = store.StateRunning
	r.save(job)

	go func() {
		defer cancel()
		if err := r.run(ctx, job); err != nil {
			return
		}
	}()
	return nil
}

// Stop halts the job in flight. Nothing already written is removed.
//
// Stopping a disc that is being read is not instant: MakeMKV finishes whatever
// the drive is doing first, which on a disc it is struggling with can take a
// little while. The message says so rather than implying it has already
// happened.
func (r *Runner) Stop() {
	job := r.Current()
	if job == nil {
		return
	}

	if job.cancel == nil {
		// Nothing is running, so there is nothing to interrupt.
		job.State = store.StateStopped
		job.Note = "Stopped."
		r.save(job)
		return
	}

	job.Log.Printf(job.Stage,
		"Stopping at your request. The drive may take a moment to finish what it is doing. Nothing already saved has been touched.")
	job.cancel()

	job.State = store.StateStopped
	if job.Note == "" {
		job.Note = "Stopped at your request."
	}
	r.save(job)
}

// run carries the job through the remaining stages.
func (r *Runner) run(ctx context.Context, job *Job) error {
	title := meta.Title{Name: job.Title, Year: job.Year}

	masterDir := title.MasterDir(r.Config.Paths.Masters)
	ripStart := time.Now()

	job.Stage = store.StageRip
	job.Progress = Progress{
		Since:     ripStart,
		Operation: "Copying the disc",
		Expected:  humanDuration(r.ripEstimate(job).Round(time.Minute)),
	}
	job.Log.Printf(store.StageRip,
		"Copying the disc. This is the slow part and usually takes %s.",
		job.Progress.Expected)
	r.save(job)

	res, err := r.Backend.Rip(ctx, makemkv.RipRequest{
		DriveIndex: 0,
		Titles:     []int{job.Plan.TitleIndex},
		OutputDir:  masterDir,
		OnProgress: func(p makemkv.Progress) {
			// Overall progress starts at zero and stays there while MakeMKV
			// works out what it is doing, so the current step stands in until
			// the total means something.
			percent := p.TotalPercent()
			if percent == 0 {
				percent = p.CurrentPercent()
			}

			job.Progress = Progress{
				Percent:   percent,
				Operation: p.Operation,
				Since:     ripStart,
				Expected:  job.Progress.Expected,
			}
			r.notify(job)
		},
		OnMessage: func(m makemkv.Message) {
			job.Log.Printf(store.StageRip, "%s", m.Text)
		},
	})
	if err != nil {
		return r.stop(job, "ARFABIT did not finish copying this disc.", detailOf(err))
	}

	job.Master = res.Files[0]
	r.Calibration.ObserveRip(job.Drive, disc.Kind(job.DiscKind), job.Plan.SourceSize, time.Since(ripStart))
	job.Log.Printf(store.StageRip, "Copied to %s.", filepath.Base(job.Master))

	// The disc has nothing left to give: everything from here happens on the
	// copy. Ejecting now rather than at the end frees the drive for hours,
	// and the alternative is a disc sitting in a machine that has finished
	// with it.
	job.Stage = store.StageEject
	r.save(job)

	ejected := eject.Eject(ctx, job.Drive)
	job.Log.Printf(store.StageEject, "%s The rest happens on the copy.", ejected.Describe(true))

	// OCR belongs here. Until it exists, the delivery carries no subtitles and
	// says so plainly rather than quietly omitting them.
	job.Stage = store.StageOCR
	if r.hasSelectedSubtitles(job) {
		job.Log.Printf(store.StageOCR,
			"This disc has subtitles, but ARFABIT cannot read them into text yet, so the movie will not have any. They are still in the master copy.")
	}

	job.Stage = store.StagePackage
	packageStart := time.Now()
	job.Progress = Progress{Since: packageStart, Operation: "Making the movie file"}
	job.Log.Printf(store.StagePackage,
		"Making the movie file for your Apple TV. Converting the picture is slow; there is nothing to do but wait.")
	r.save(job)

	delivery, err := r.packageMaster(ctx, job, title)
	if err != nil {
		return r.stop(job, "ARFABIT did not finish making the movie file.", detailOf(err))
	}
	job.Delivery = delivery

	job.Stage = store.StageDeliver
	if err := r.deliver(job, title); err != nil {
		return r.stop(job, "ARFABIT made the movie but could not put it in your library.", err.Error())
	}

	job.State = store.StateDone
	job.Note = fmt.Sprintf("%s is ready.", job.Title)
	job.Progress = Progress{Percent: 100}
	r.save(job)

	return nil
}

// packageMaster encodes or copies the master into the delivery file.
func (r *Runner) packageMaster(ctx context.Context, job *Job, title meta.Title) (string, error) {
	info, err := ffmpeg.Probe(ctx, job.Master)
	if err != nil {
		return "", err
	}

	video := info.VideoStream()
	if video == nil {
		return "", errors.New("the copied file has no picture")
	}

	outDir := title.LibraryDir(r.Config.Paths.Library)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, title.VideoName(job.Plan.Profile))

	req := ffmpeg.EncodeRequest{
		Input:            job.Master,
		Output:           out,
		VideoSourceIndex: video.Index,
		Video: ffmpeg.VideoPlan{
			Copy:   job.Plan.VideoCopy,
			CRF:    job.Plan.CRF,
			Preset: ffmpeg.Preset(job.Plan.Preset),
		},
		HDR:      video.HDR,
		Color:    video.ColorInfo,
		Chapters: true,
		Audio:    r.audioTracks(job, info),
	}

	// An HDR source whose metadata could not be read must stop rather than
	// produce a grey picture nobody would notice until playback (§9).
	if video.ColorInfo.IsHDR() && !req.Video.Copy && !video.HDR.HasMasteringDisplay() {
		return "", errors.New("this disc says it is HDR but its colour information could not be read, and encoding without it would wash the picture out")
	}
	if video.ColorInfo.IsHDR() {
		job.Plan.HDR = true
		job.Log.Printf(store.StagePackage, "Keeping the HDR picture information.")
	}

	args, err := req.Args()
	if err != nil {
		return "", err
	}

	start := time.Now()
	err = ffmpeg.Run(ctx, args, ffmpeg.RunOptions{
		Duration: time.Duration(info.Duration * float64(time.Second)),
		OnProgress: func(p ffmpeg.Progress) {
			job.Progress = Progress{
				Percent:   p.Percent(),
				Operation: "Making the movie file",
				Remaining: humanDuration(p.Remaining().Round(time.Minute)),
				Since:     start,
			}
			r.notify(job)
		},
	})
	if err != nil {
		return "", err
	}

	if info, err := os.Stat(out); err == nil && !job.Plan.VideoCopy {
		r.Calibration.ObserveEncode(job.Plan.Preset, video.Height, info.Size(),
			time.Duration(0), time.Since(start))
	}

	return out, nil
}

// audioTracks turns the Plan's audio choices into encoder tracks.
func (r *Runner) audioTracks(job *Job, info *ffmpeg.MediaInfo) []ffmpeg.AudioTrack {
	sources := info.StreamsOfKind("audio")
	var tracks []ffmpeg.AudioTrack

	for _, planned := range job.Plan.Audio {
		if !planned.Selected {
			continue
		}
		// The master contains only the streams MakeMKV kept, so positions are
		// matched by order rather than by the disc's own indexes.
		idx := audioIndexFor(sources, planned, len(tracks))
		if idx < 0 {
			continue
		}

		track := ffmpeg.AudioTrack{
			SourceIndex: idx,
			Copy:        planned.Copy,
			Codec:       planned.Codec,
			Bitrate:     planned.Bitrate,
			Lang:        planned.Lang,
			Title:       shortTrackName(planned),
			Default:     len(tracks) == 0,
		}
		// Only a stereo downmix asks for a channel count. Surround keeps the
		// source's own layout, and the codec was chosen in the Plan to be one
		// that can hold it.
		if planned.Stereo || (planned.Channels <= 2 && !planned.Copy) {
			track.Channels = 2
		}
		tracks = append(tracks, track)
	}

	return tracks
}

// audioIndexFor maps a planned track onto a stream in the copied file.
func audioIndexFor(sources []ffmpeg.Stream, planned store.PlannedAudio, position int) int {
	if len(sources) == 0 {
		return -1
	}
	// The stereo downmix is derived from the track before it.
	if planned.Stereo && position > 0 {
		return sources[0].Index
	}
	for _, s := range sources {
		if s.Lang == planned.Lang {
			return s.Index
		}
	}
	return sources[0].Index
}

// deliver records the finished file and copies it onward if asked.
func (r *Runner) deliver(job *Job, title meta.Title) error {
	info, err := os.Stat(job.Delivery)
	if err != nil {
		return err
	}

	if err := r.Store.AppendLibrary(store.LibraryEntry{
		Title:     title.Name,
		Year:      title.Year,
		Edition:   job.Plan.Profile,
		Path:      job.Delivery,
		Size:      info.Size(),
		Node:      r.Config.Node.ID,
		JobID:     job.ID,
		Delivered: time.Now(),
	}); err != nil {
		return err
	}

	job.Log.Printf(store.StageDeliver, "Saved %s (%s).", filepath.Base(job.Delivery), HumanBytes(info.Size()))
	return nil
}

// surroundNote explains a disc whose surround sound cannot be carried across.
//
// Some discs offer surround only in formats an Apple TV cannot decode, with
// Dolby available in stereo alone. ARFABIT then keeps the stereo track, which
// is bit-perfect, rather than converting surround down to it — but the loss is
// worth saying out loud rather than leaving to be noticed on the sofa.
func surroundNote(plan *store.Plan) string {
	var chosen *store.PlannedAudio
	var surroundAvailable bool

	for i, a := range plan.Audio {
		if a.Selected && chosen == nil {
			chosen = &plan.Audio[i]
		}
		if a.Layout != "" && a.Layout != "stereo" {
			surroundAvailable = true
		}
	}

	if chosen == nil || !surroundAvailable || chosen.Layout != "stereo" {
		return ""
	}
	return "This disc's surround sound is in a format an Apple TV cannot play, and its Dolby track is stereo only, so the movie will be in stereo."
}

// shortTrackName is what the track is called in the player's own menu, where
// there is no room for the Plan's full explanation.
func shortTrackName(planned store.PlannedAudio) string {
	return fmt.Sprintf("%s %s", languageName(planned.Lang), planned.Layout)
}

func (r *Runner) hasSelectedSubtitles(job *Job) bool {
	for _, s := range job.Plan.Subtitles {
		if s.Selected {
			return true
		}
	}
	return false
}

// ripEstimate is how long copying this disc is expected to take.
func (r *Runner) ripEstimate(job *Job) time.Duration {
	if job.Plan == nil {
		return 0
	}
	return r.Calibration.EstimateRip(job.Drive, disc.Kind(job.DiscKind), job.Plan.SourceSize).Time
}

// stageWords names a stage the way the page does, for messages that mention
// what something is busy with.
func stageWords(stage store.Stage) string {
	switch stage {
	case store.StageScan:
		return "Reading the disc"
	case store.StageRip:
		return "Copying the disc"
	case store.StageOCR:
		return "Reading the subtitles"
	case store.StagePackage:
		return "Making the movie file"
	case store.StageDeliver:
		return "Putting it in your library"
	case store.StageEject:
		return "Ejecting the disc"
	default:
		return "Working"
	}
}

// readFailureNote explains a disc that could not be read.
//
// Only one thing is said with any confidence, and only on a signature seen in
// practice: repeated SCSI timeouts mean the drive gave up on the disc, which
// is a physical problem a person can act on. Everything else gets the plain
// statement and the raw output (§15).
func readFailureNote(err error) string {
	var mkErr *makemkv.Error
	if errors.As(err, &mkErr) {
		for _, m := range mkErr.Messages {
			if strings.Contains(m.Text, "TIMEOUT ON LOGICAL UNIT") {
				return "The drive could not read part of this disc. " +
					"Discs that are dirty or scratched often do this, and so do drives that are not getting enough power. " +
					"Wiping the disc and trying again is usually worth a go."
			}
		}
	}
	return "ARFABIT did not finish reading this disc."
}

// stop ends a job without describing it as a failure, and keeps the raw
// account of what happened (§15).
func (r *Runner) stop(job *Job, note, detail string) error {
	job.State = store.StateStopped
	job.Note = note
	job.Detail = detail
	job.Log.Detail(job.Stage, note, detail)
	r.save(job)
	return errors.New(note)
}

// SetCurrentForTest installs a job without running a scan.
func (r *Runner) SetCurrentForTest(job *Job) { r.setCurrent(job) }

// retirePreviousScan closes off a Plan nobody acted on.
func (r *Runner) retirePreviousScan() {
	previous := r.Current()
	if previous == nil || previous.State != store.StateWaiting {
		return
	}

	previous.State = store.StateStopped
	previous.Note = "Not started."
	_ = r.Store.SaveJob(previous.Job)
}

func (r *Runner) setCurrent(job *Job) {
	r.mu.Lock()
	r.current = job
	r.mu.Unlock()
}

func (r *Runner) save(job *Job) {
	_ = r.Store.SaveJob(job.Job)
	r.notify(job)
}

func (r *Runner) notify(job *Job) {
	if r.OnUpdate != nil {
		r.OnUpdate(job)
	}
}

// detailOf renders whatever an error carries, preserving raw output.
func detailOf(err error) string {
	var mkErr *makemkv.Error
	if errors.As(err, &mkErr) {
		var b []byte
		if plain, ok := mkErr.Explain(); ok {
			b = append(b, plain...)
			b = append(b, '\n', '\n')
		}
		for _, m := range mkErr.Messages {
			b = append(b, fmt.Sprintf("[%d] %s\n", m.Code, m.Text)...)
		}
		return string(b)
	}

	var runErr *ffmpeg.RunError
	if errors.As(err, &runErr) {
		return runErr.Output
	}

	return err.Error()
}

// titleFrom works out what to call the movie.
//
// MakeMKV's own disc name is usually the best source, but it arrives as the
// disc author wrote it: shouted, and sometimes with the format tacked on, as in
// "THE MANDALORIAN AND GROGU - BLU-RAY". Either way it is tidied before it
// becomes a folder name.
// bestLabel is whichever of the disc's names is more likely to be searchable.
func bestLabel(d *disc.Disc) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Label
}

func titleFrom(d *disc.Disc) string {
	if name := meta.CleanDiscLabel(d.Name); name != "" {
		return name
	}
	return meta.CleanDiscLabel(d.Label)
}

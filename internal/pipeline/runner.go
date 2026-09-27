package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/drive"
	"github.com/arfabit/arfabit/internal/eject"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/lab"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
)

// Runner carries one disc through the pipeline.
type Runner struct {
	Config      config.Config
	Store       *store.Store
	Backend     *makemkv.Backend
	Calibration *Calibration

	// Slots limits how much converting happens at once. Discs keep going in
	// regardless; it is the processor that has to take turns.
	Slots *Slots

	// Hold is how long a project waits in the line before it may start,
	// while it can still be moved or stopped at no cost. Zero means none.
	Hold time.Duration

	// ForPlan is what a new Plan starts from: the default blueprint, or the
	// defaults when none is chosen. Set by whatever owns the blueprints, since
	// which one is the default can be changed while ARFABIT is running.
	ForPlan func() config.Blueprint

	// OCR reads picture subtitles into text: the operating system's own
	// reader, or nil where there is none (§10).
	OCR ocr.Reader

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
	pending *Job   // scanned, waiting for the user to say go
	active  []*Job // being worked on

	// planned is the disc last started. Its Plan can still be changed while
	// it is copied, and its film's while the film waits (editing.go).
	planned *Job

	// readings are the OCR tasks running, by the track they read, so a film
	// wanting that track's SRT can wait for it.
	readings map[string]*Job
}

// TranscodeHold is how long a new project waits before it may start.
const TranscodeHold = 10 * time.Second

// Job is a job record plus the live state the UI needs.
type Job struct {
	*store.Job

	Log      *Log     `json:"-"`
	Progress Progress `json:"progress"`
	Space    Space    `json:"space"`

	// File is the name of the file this job is writing or about to write,
	// so the page can say exactly what is being made. Empty when there is
	// none yet, or when it is about to make several.
	File string `json:"file,omitempty"`

	// Comparison is what a set of test clips came to, for lab jobs.
	Comparison lab.Comparison `json:"comparison,omitempty"`

	cancel context.CancelFunc

	// ripped is closed when a rip is over, however it ended, so a transcode
	// waiting on it can go on or give up. Nil for anything but a rip.
	ripped chan struct{}

	// read is closed when an OCR task is over, however it ended. Nil for
	// anything else.
	read chan struct{}

	// film is the film planned with a disc, a task of its own, while it can
	// still be changed. copied is set once the disc is copied, when the
	// copy's name and the subtitles to read are settled.
	film   *Job
	copied bool

	// discTracks are what a film planned with its disc was planned from,
	// and bound what its Original holds, once it exists: the film's line
	// items are pointed from one to the other (bindToOriginal).
	discTracks []Track
	bound      []Track
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

	// Rate is how fast the disc is being read or the file written, averaged
	// over the last few seconds. Empty until there is enough to measure.
	//
	// A rate is worth showing because it is comparable: a percentage climbing
	// slowly means nothing on its own, but 2.6 MB/s against 12 MB/s is an
	// answer.
	Rate string `json:"rate,omitempty"`
}

// Current returns the job waiting on a decision, or the newest running one.
//
// The page shows one Plan at a time, so "current" is whatever is asking for an
// answer.
func (r *Runner) Current() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pending != nil {
		return r.pending
	}
	if len(r.active) > 0 {
		return r.active[len(r.active)-1]
	}
	return nil
}

// Active returns every job being worked on, oldest first.
func (r *Runner) Active() []*Job {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]*Job, len(r.active))
	copy(out, r.active)
	return out
}

// DriveIsBusy reports whether a job currently needs the disc drive.
//
// Only reading and copying need it. Everything after the disc comes out
// happens on the copy, which is what lets the next disc go in while the last
// one is still being converted.
func (r *Runner) DriveIsBusy() *Job {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pending != nil && r.pending.State == store.StateRunning {
		return r.pending
	}
	for _, job := range r.active {
		switch job.Stage {
		case store.StageScan, store.StageRip:
			return job
		}
	}
	return nil
}

// finish moves a job off the active list.
func (r *Runner) finish(job *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, active := range r.active {
		if active == job {
			r.active = append(r.active[:i], r.active[i+1:]...)
			break
		}
	}
}

// begin moves the pending job onto the active list.
func (r *Runner) begin(job *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pending == job {
		r.pending = nil
	}
	r.active = append(r.active, job)
}

// Scan reads the disc and builds a Plan from the blueprint used by default,
// or the defaults, stopping short of doing anything to it. Nothing starts
// until the user says so (§8), or the drive is set to start on its own.
func (r *Runner) Scan(ctx context.Context, drive disc.Drive) (*Job, error) {
	return r.ScanWith(ctx, drive, r.blueprint())
}

// ScanWith is Scan with the Plan filled in from a blueprint of its own.
func (r *Runner) ScanWith(ctx context.Context, drive disc.Drive, blueprint config.Blueprint) (*Job, error) {
	// The drive can only do one thing at a time, but only reading and copying
	// need it. A disc that has been ejected leaves its job converting on its
	// own, and the next disc can go straight in.
	if busy := r.DriveIsBusy(); busy != nil {
		return nil, fmt.Errorf(
			"the drive is busy with %s (%s). It will be free once that disc comes out",
			busy.Name(), strings.ToLower(stageWords(busy.Stage)))
	}

	// A scan that was never started is finished with rather than left hanging
	// as though it were still expecting an answer. Reading a disc again is
	// how somebody says they have changed their mind.
	r.retirePreviousScan()

	rec := store.NewJob(store.NewJobID(time.Now(), drive.Label))
	rec.DiscLabel = drive.Label
	rec.Drive = drive.Device
	rec.DriveName = drive.Name
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
	log.Describe(rec.ID, rec.DiscLabel)

	job := &Job{Job: rec, Log: log, cancel: cancel}
	r.setPending(job)

	// Tell the page a disc is being read before doing it, not after.
	r.notify(job)

	job.Progress = Progress{Since: time.Now(), Operation: "Reading the disc"}
	log.Printf(store.StageScan, "Reading the disc in %s. This takes a minute or two.", drive.Name)

	// While the system holds the disc open, MakeMKV cannot claim the drive
	// exclusively, and an encrypted disc then reads at roughly the speed it
	// would play at — hours rather than tens of minutes. Letting go of it
	// leaves the disc exactly where it is.
	r.freeTheDisc(ctx, job, drive.Device)

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

	plan, err := BuildPlan(d, sel, blueprint, r.OCR != nil)
	if err != nil {
		r.stop(job, "ARFABIT could not work out what to do with this disc.", err.Error())
		return job, err
	}

	title := d.Titles[sel.Feature]
	rec.Plan = plan
	rec.Stage = store.StagePlan
	rec.State = store.StateWaiting
	rec.Title = titleFrom(d)
	log.Describe(rec.ID, rec.Title)

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
	ripEst := r.Calibration.EstimateRip(DriveKey(drive.Name, drive.Device), d.Kind, title.SizeBytes)
	plan.RipTime = ripEst.Time
	r.Reestimate(job)

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
	r.mu.Lock()
	job := r.pending
	r.mu.Unlock()

	if job == nil {
		return errors.New("there is no disc waiting")
	}
	if job.State == store.StateRunning {
		return errors.New("this disc is already being worked on")
	}
	if busy := r.DriveIsBusy(); busy != nil && busy != job {
		return fmt.Errorf("the drive is busy with %s", busy.Name())
	}
	if !job.Space.Fits {
		return errors.New(job.Space.Describe())
	}
	if err := wouldReplace(r.Existing(job)); err != nil {
		return err
	}
	if job.Plan != nil && job.Plan.Convert {
		if job.Plan.Project == nil {
			return errors.New("there is nothing planned to make from it; turn off Also make a file from it to copy the disc only")
		}
		check := *job.Plan.Project
		if err := checkProject(&check, r.OCR != nil); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(parent)
	job.cancel = cancel
	job.State = store.StateRunning
	job.ripped = make(chan struct{})
	r.begin(job)
	r.mu.Lock()
	r.planned = job
	r.mu.Unlock()
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)
		defer close(job.ripped)
		_ = r.run(ctx, job)
	}()

	// A transcode planned with the disc joins the queue now, so it is seen
	// waiting behind the rip rather than appearing out of nowhere later.
	if job.Plan.Convert {
		r.followRip(parent, job)
	}
	return nil
}

// followRip makes the package planned with a disc into a job of its own,
// which waits for the rip to finish and then makes its file from the original.
//
// It is two jobs because it is two pieces of work, needing different things:
// the rip needs the drive, the package needs the processor. Planning both at
// once is only convenient, since the Plan is where the disc's contents are
// known. The package is copied from the Plan as it stood when started.
func (r *Runner) followRip(parent context.Context, rip *Job) {
	r.mu.Lock()
	pkg := *rip.Plan.Project
	pkg.Items = slices.Clone(rip.Plan.Project.Items)
	pkg.Containers = slices.Clone(rip.Plan.Project.Containers)
	r.mu.Unlock()

	rec := store.NewJob(store.NewJobID(time.Now(), rip.Title+" package"))
	rec.Kind = store.KindConvert
	rec.From = rip.ID
	rec.Title, rec.Year = rip.Title, rip.Year
	rec.DiscName, rec.DiscLabel, rec.DiscKind = rip.DiscName, rip.DiscLabel, rip.DiscKind
	rec.Project = &pkg
	rec.Stage = store.StageQueued

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		rip.Log.Printf(store.StagePlan, "What was to be made from it could not be set up, so only the original will be made: %v", err)
		return
	}
	log.Describe(rec.ID, rec.Title)
	log.Printf(store.StageQueued, "Waiting for the original of %s. This starts once the disc is copied.", rip.Title)

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel, discTracks: rip.Plan.Tracks}
	job.File = meta.Title{Name: rec.Title, Year: rec.Year}.VideoName(pkg.Edition)
	job.Progress = Progress{Since: time.Now(), Operation: "Waiting for its original"}

	r.begin(job)
	r.mu.Lock()
	rip.film = job
	r.mu.Unlock()
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)

		select {
		case <-rip.ripped:
		case <-ctx.Done():
			r.stop(job, "Stopped before it began.", "")
			return
		}

		// A rip that was stopped or did not finish leaves nothing to work
		// from, so this goes too, and says why.
		if rip.State != store.StateDone || rip.Original == "" {
			r.stop(job, fmt.Sprintf("Not started, because %s was not copied.", rip.Title), "")
			return
		}
		job.Original = rip.Original

		// The package was planned from the scan; the original numbers its
		// tracks its own way. It can still be changed until it starts, and a
		// change is pointed at the original in the same way (editing.go).
		info, err := ffmpeg.Probe(ctx, job.Original)
		if err != nil {
			r.stop(job, "ARFABIT could not read the original.", err.Error())
			return
		}
		tracks := OriginalTracks(info)
		r.mu.Lock()
		err = bindToOriginal(job.Project, job.discTracks, tracks)
		if err == nil {
			job.bound = tracks
		}
		r.mu.Unlock()
		if err != nil {
			r.stop(job, sentence(err.Error())+". Nothing was made. The original is kept, so anything can be made from it in Projects.", "")
			return
		}
		job.Log.Printf(store.StageQueued, "The original is ready.")
		r.runPackage(ctx, job, r.Config.Paths.Library, 0)
	}()
}

// StopJob halts one job by its id.
func (r *Runner) StopJob(id string) error {
	r.mu.Lock()
	var target *Job
	if r.pending != nil && r.pending.ID == id {
		target = r.pending
	}
	for _, job := range r.active {
		if job.ID == id {
			target = job
		}
	}
	r.mu.Unlock()

	if target == nil {
		return fmt.Errorf("there is nothing here called %s", id)
	}
	r.stopJob(target)
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
	r.stopJob(job)
}

// Queued reports how many jobs are waiting for the processor.
func (r *Runner) Queued() int {
	if r.Slots == nil {
		return 0
	}
	_, waiting := r.Slots.Busy()
	return waiting
}

func (r *Runner) stopJob(job *Job) {

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

	// The copy is made in the film's own folder, where the original stays
	// beside the films made from it (§6).
	folder := title.LibraryDir(r.Config.Paths.Library)
	_, statErr := os.Stat(folder)
	madeFolder := os.IsNotExist(statErr)
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

	job.File = job.Plan.RipName

	var ripRate rateTracker
	speed := speedSampler{job: job}
	job.ReadSpeed = nil

	res, err := r.Backend.Rip(ctx, makemkv.RipRequest{
		DriveIndex: 0,
		Titles:     []int{job.Plan.TitleIndex},
		OutputDir:  folder,
		OnProgress: func(p makemkv.Progress) {
			// Overall progress starts at zero and stays there while MakeMKV
			// works out what it is doing, so the current step stands in until
			// the total means something.
			percent := p.TotalPercent()
			if percent == 0 {
				percent = p.CurrentPercent()
			}

			// MakeMKV reports progress as a fraction, so bytes read is that
			// fraction of the title. Near enough to measure a speed with,
			// which is the only figure here that can be compared to anything.
			done := int64(percent / 100 * float64(job.Plan.SourceSize))

			rate := ripRate.Observe(done)
			job.Progress = Progress{
				Percent:   percent,
				Operation: p.Operation,
				Since:     ripStart,
				Expected:  job.Progress.Expected,
				Rate:      HumanRate(rate),
			}
			// Only the overall figure measures the disc. The current step's
			// percentage belongs to whatever MakeMKV is doing first, and a
			// speed worked out from it would be made up.
			if total := p.TotalPercent(); total > 0 {
				speed.observe(time.Since(ripStart), int64(total/100*float64(job.Plan.SourceSize)))
			}
			r.notify(job)
		},
		OnMessage: func(m makemkv.Message) {
			job.Log.Printf(store.StageRip, "%s", m.Text)
		},
	})
	if err != nil {
		note := "ARFABIT did not finish copying this disc."
		if _, statErr := os.Stat(filepath.Join(folder, job.Plan.RipName)); job.Plan.RipName != "" && statErr == nil {
			note += fmt.Sprintf(" The unfinished copy is in %s, as %s. Nothing was removed.", folder, job.Plan.RipName)
		}
		return r.stop(job, note, detailOf(err))
	}

	r.Calibration.ObserveRip(DriveKey(job.DriveName, job.Drive), disc.Kind(job.DiscKind), job.Plan.SourceSize, time.Since(ripStart))

	job.Original = r.placeOriginal(job, res.Files[0], folder, madeFolder)
	job.File = filepath.Base(job.Original)

	// The disc has nothing left to give: everything from here happens on the
	// copy. Ejecting now rather than at the end frees the drive for hours,
	// and the alternative is a disc sitting in a machine that has finished
	// with it.
	job.Stage = store.StageEject
	r.save(job)

	ejected := eject.Eject(ctx, job.Drive)
	job.Log.Printf(store.StageEject, "%s", ejected.Describe(true))

	// The rip is finished once the original exists and the disc is out. A
	// transcode planned with it is a job of its own, waiting on this one.
	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	r.mu.Lock()
	convert := job.Plan.Convert
	r.mu.Unlock()
	job.Note = fmt.Sprintf("%s is copied.", job.Title)
	if !convert {
		job.Note += " Make anything from it whenever you like, in Projects."
	}
	job.Log.Printf(store.StageEject, "%s", job.Note)
	r.save(job)

	// Its subtitles are read from the original, each track a task of its
	// own, while the film waits its turn at the processor.
	r.readAfterCopy(ctx, job)
	return nil
}

// placeOriginal settles the copy MakeMKV made in folder as the Original, and
// returns where it is. The name, and what to read, could be changed until now
// (editing.go). A name changed while copying moves the copy to the folder for
// that name, and the folder made for it under the old one goes, if nothing
// else is in it.
func (r *Runner) placeOriginal(job *Job, copied, folder string, madeFolder bool) string {
	r.mu.Lock()
	job.copied = true
	title := meta.Title{Name: job.Title, Year: job.Year}
	r.mu.Unlock()

	into := title.LibraryDir(r.Config.Paths.Library)
	if into != folder {
		if err := os.MkdirAll(into, 0o755); err != nil {
			into = folder
		}
	}
	original := r.nameOriginal(job, copied, filepath.Join(into, title.OriginalName()))
	if into != folder && madeFolder && filepath.Dir(original) == into {
		_ = os.Remove(folder) // only ever an empty folder: os.Remove takes nothing else
	}
	return original
}

// summarise puts what a Plan's project makes where the estimate reads it: its
// video, and its audio, copied or converted. It reports whether there is no
// video.
func summarise(plan *store.Plan) (noVideo bool) {
	noVideo = true
	plan.Audio = nil
	if plan.Project == nil {
		return noVideo
	}
	if videos := plan.Project.ItemsOf(store.KindVideo); len(videos) == 1 {
		noVideo = false
		plan.VideoCopy = videos[0].Action == store.ActionCopy
		if !plan.VideoCopy {
			plan.CRF, plan.Preset = videos[0].CRF, videos[0].Preset
		}
	}
	for _, it := range plan.Project.ItemsOf(store.KindAudio) {
		plan.Audio = append(plan.Audio, store.PlannedAudio{Selected: true, Copy: it.Action == store.ActionCopy})
	}
	return noVideo
}

// nameOriginal renames the file MakeMKV made to the original's own name, beside
// the films made from it, and returns where it is. A rename, never a
// removal: if something already has that name, the copy keeps MakeMKV's.
func (r *Runner) nameOriginal(job *Job, copied, original string) string {
	if err := refuseToReplace(original); err != nil {
		job.Log.Printf(store.StageRip, "Copied to %s. %s is already there, so the copy keeps the name MakeMKV gave it.",
			filepath.Base(copied), filepath.Base(original))
		return copied
	}
	if err := os.Rename(copied, original); err != nil {
		job.Log.Detail(store.StageRip, fmt.Sprintf("Copied to %s. ARFABIT could not rename it to %s, so it keeps the name MakeMKV gave it.",
			filepath.Base(copied), filepath.Base(original)), err.Error())
		return copied
	}
	job.Log.Printf(store.StageRip, "Copied to %s.", filepath.Base(original))
	return original
}

// Reestimate works a waiting Plan's estimate out again after it changes: the
// picture its package makes, and whether there is a package at all.
func (r *Runner) Reestimate(job *Job) {
	plan := job.Plan
	if plan == nil {
		return
	}

	noVideo := summarise(plan)

	var packEst Estimate
	if plan.Convert && plan.Seconds > 0 {
		duration := time.Duration(plan.Seconds) * time.Second
		if noVideo {
			packEst = Estimate{Size: audioBytes(plan, duration), Time: duration / 20}
		} else {
			packEst = r.Calibration.EstimatePackage(plan, duration)
		}
	}
	plan.EstimatedSize = packEst.Size
	plan.EstimatedTime = plan.RipTime + packEst.Time

	// The original and the film both exist at once, so both must fit.
	job.Space, _ = CheckSpace(plan.SourceSize+packEst.Size, r.Config.Paths.Library)
}

// speedEvery is how long each point on the read-speed graph drawn after a
// copy covers. Half a minute is about three hundred points for a long disc:
// more than enough to see its shape, and small enough to keep in the job
// record.
const speedEvery = 30 * time.Second

// speedSampler records a job's read speed as averages over half a minute: all
// that was read in each stretch, divided by how long the stretch took. A speed
// caught at one moment every half minute would only say what the drive was
// doing at those moments, and a drive's speed swings from one second to the
// next.
type speedSampler struct {
	job *Job

	started bool
	since   time.Duration // how far into the stage this stretch began
	read    int64         // bytes read when it began
}

// observe notes how much had been read by a moment into the stage, and keeps
// a point each time a stretch is complete.
func (s *speedSampler) observe(into time.Duration, read int64) {
	// The first reading only starts the clock: nothing is known about how
	// fast it got there. A count that goes backwards means MakeMKV has started
	// counting again, so the stretch starts again with it.
	if !s.started || read < s.read {
		s.started, s.since, s.read = true, into, read
		return
	}

	took := into - s.since
	if took < speedEvery {
		return
	}

	s.job.ReadSpeed = append(s.job.ReadSpeed, store.SpeedSample{
		Seconds:     int(into / time.Second),
		MBPerSecond: math.Round(float64(read-s.read)/took.Seconds()/100_000) / 10,
	})
	s.since, s.read = into, read
}

// deliver records the finished file and copies it onward if asked.
func (r *Runner) deliver(job *Job, title meta.Title, edition string) error {
	info, err := os.Stat(job.Delivery)
	if err != nil {
		return err
	}

	if err := r.Store.AppendLibrary(store.LibraryEntry{
		Title:     title.Name,
		Year:      title.Year,
		Edition:   edition,
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

// ripEstimate is how long copying this disc is expected to take.
func (r *Runner) ripEstimate(job *Job) time.Duration {
	if job.Plan == nil {
		return 0
	}
	return r.Calibration.EstimateRip(DriveKey(job.DriveName, job.Drive), disc.Kind(job.DiscKind), job.Plan.SourceSize).Time
}

// freeTheDisc asks the operating system to let go of the disc.
//
// Nothing here is fatal: if it cannot be done the disc is still readable, just
// slowly, and saying so is more use than refusing to continue.
func (r *Runner) freeTheDisc(ctx context.Context, job *Job, device string) {
	state := drive.Check(ctx, device)
	if !state.Mounted {
		return
	}

	job.Log.Printf(job.Stage, "Your computer has this disc open, which would make reading it much slower. Letting go of it.")

	after := drive.Unmount(ctx, device)

	switch {
	case !after.Present:
		// Letting go of a volume and ejecting a disc are meant to be
		// different things. If this drive treats them as the same, say so
		// plainly rather than leaving somebody watching an empty drive.
		job.Log.Detail(job.Stage,
			"The disc came out when your computer let go of it. Put it back in and ARFABIT will read it as it is.",
			after.Output)

	case after.Mounted:
		job.Log.Detail(job.Stage,
			"Your computer would not let go of the disc, so reading it will be slower than it could be.",
			after.Output)

	default:
		job.Log.Printf(job.Stage, "Done. The disc is still in the drive.")
	}
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
	case store.StageQueued:
		return "Waiting its turn"
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
func (r *Runner) SetCurrentForTest(job *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if job.State == store.StateRunning {
		r.active = append(r.active, job)
		r.pending = nil
		return
	}
	r.pending = job
}

// retirePreviousScan closes off a Plan nobody acted on.
// blueprint is what a new Plan starts from: the blueprint used by default, or
// the defaults when none is.
func (r *Runner) blueprint() config.Blueprint {
	if r.ForPlan != nil {
		return r.ForPlan()
	}
	return r.Config.Plain()
}

func (r *Runner) retirePreviousScan() {
	r.mu.Lock()
	previous := r.pending
	r.mu.Unlock()

	if previous == nil || previous.State != store.StateWaiting {
		return
	}

	previous.State = store.StateStopped
	previous.Note = "Not started."
	_ = r.Store.SaveJob(previous.Job)
}

func (r *Runner) setPending(job *Job) {
	r.mu.Lock()
	r.pending = job
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

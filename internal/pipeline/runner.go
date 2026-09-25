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

	// Hold is how long a Transcode waits in the line before it may start,
	// while it can still be moved or stopped at no cost. Zero means none.
	Hold time.Duration

	// ForPlan is what a new Plan starts from: the default blueprint, or the
	// defaults when none is chosen. Set by whatever owns the blueprints, since
	// which one is the default can be changed while ARFABIT is running.
	ForPlan func() config.Blueprint

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
}

// TranscodeHold is how long a new Transcode waits before it may start.
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

// Scan reads the disc and builds a Plan, stopping short of doing anything to
// it. Nothing starts until the user says so (§8).
func (r *Runner) Scan(ctx context.Context, drive disc.Drive) (*Job, error) {
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

	plan, err := BuildPlan(d, sel, r.blueprint())
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
		if job.Plan.Package == nil {
			return errors.New("there is no package planned; turn off Plan a transcode to copy the disc only")
		}
		check := *job.Plan.Package
		if err := checkPackage(&check); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(parent)
	job.cancel = cancel
	job.State = store.StateRunning
	job.ripped = make(chan struct{})
	r.begin(job)
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
// which waits for the rip to finish and then makes its file from the master.
//
// It is two jobs because it is two pieces of work, needing different things:
// the rip needs the drive, the package needs the processor. Planning both at
// once is only convenient, since the Plan is where the disc's contents are
// known. The package is copied from the Plan as it stood when started.
func (r *Runner) followRip(parent context.Context, rip *Job) {
	pkg := *rip.Plan.Package
	pkg.Items = slices.Clone(rip.Plan.Package.Items)
	pkg.Containers = slices.Clone(rip.Plan.Package.Containers)

	rec := store.NewJob(store.NewJobID(time.Now(), rip.Title+" package"))
	rec.Kind = store.KindConvert
	rec.From = rip.ID
	rec.Title, rec.Year = rip.Title, rip.Year
	rec.DiscName, rec.DiscLabel, rec.DiscKind = rip.DiscName, rip.DiscLabel, rip.DiscKind
	rec.Package = &pkg
	rec.Stage = store.StageQueued

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		rip.Log.Printf(store.StagePlan, "The package could not be set up, so only the master will be made: %v", err)
		return
	}
	log.Describe(rec.ID, rec.Title)
	log.Printf(store.StageQueued, "Waiting for the master of %s. This starts once the disc is copied.", rip.Title)

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel}
	job.File = meta.Title{Name: rec.Title, Year: rec.Year}.VideoName(pkg.Edition)
	job.Progress = Progress{Since: time.Now(), Operation: "Waiting for its master"}

	r.begin(job)
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
		if rip.State != store.StateDone || rip.Master == "" {
			r.stop(job, fmt.Sprintf("Not started, because %s was not copied.", rip.Title), "")
			return
		}
		job.Master = rip.Master

		// The package was planned from the scan; the master numbers its
		// tracks its own way.
		info, err := ffmpeg.Probe(ctx, job.Master)
		if err != nil {
			r.stop(job, "ARFABIT could not read the master.", err.Error())
			return
		}
		if err := bindToMaster(job.Package, MasterTracks(info)); err != nil {
			r.stop(job, sentence(err.Error())+". Nothing was made. The master is kept, so a package can be made from it in Packages.", "")
			return
		}
		job.Log.Printf(store.StageQueued, "The master is ready.")
		r.runPackage(ctx, job, r.configuredDirs(), 0)
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

	job.File = job.Plan.MasterName

	var ripRate rateTracker
	speed := speedSampler{job: job}
	job.ReadSpeed = nil

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
		return r.stop(job, "ARFABIT did not finish copying this disc.", detailOf(err))
	}

	job.Master = res.Files[0]
	job.File = filepath.Base(job.Master)
	r.Calibration.ObserveRip(DriveKey(job.DriveName, job.Drive), disc.Kind(job.DiscKind), job.Plan.SourceSize, time.Since(ripStart))
	job.Log.Printf(store.StageRip, "Copied to %s.", filepath.Base(job.Master))

	// The disc has nothing left to give: everything from here happens on the
	// copy. Ejecting now rather than at the end frees the drive for hours,
	// and the alternative is a disc sitting in a machine that has finished
	// with it.
	job.Stage = store.StageEject
	r.save(job)

	ejected := eject.Eject(ctx, job.Drive)
	job.Log.Printf(store.StageEject, "%s", ejected.Describe(true))

	// The rip is finished once the master exists and the disc is out. A
	// transcode planned with it is a job of its own, waiting on this one.
	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%s is copied.", job.Title)
	if !job.Plan.Convert {
		job.Note += " Transcode it whenever you like, from Labs."
	}
	job.Log.Printf(store.StageEject, "%s", job.Note)
	r.save(job)
	return nil
}

// Reestimate works a waiting Plan's estimate out again after it changes: the
// picture its package makes, and whether there is a package at all.
func (r *Runner) Reestimate(job *Job) {
	plan := job.Plan
	if plan == nil {
		return
	}

	if plan.Package != nil {
		if videos := plan.Package.ItemsOf(store.KindVideo); len(videos) == 1 {
			plan.VideoCopy = videos[0].Action == store.ActionCopy
			if !plan.VideoCopy {
				plan.CRF, plan.Preset = videos[0].CRF, videos[0].Preset
			}
		}
	}

	var packEst Estimate
	if plan.Convert && plan.Seconds > 0 {
		packEst = r.Calibration.EstimatePackage(plan, time.Duration(plan.Seconds)*time.Second)
	}
	plan.EstimatedSize = packEst.Size
	plan.EstimatedTime = plan.RipTime + packEst.Time

	// The master and the package's file both exist at once, so both must fit.
	job.Space, _ = CheckSpace(plan.SourceSize+packEst.Size, r.Config.Paths.Masters, r.Config.Paths.Library)
}

// UpdatePackage replaces the waiting Plan's package with one the user has
// changed.
func (r *Runner) UpdatePackage(pkg store.Package) error {
	r.mu.Lock()
	job := r.pending
	r.mu.Unlock()
	if job == nil || job.Plan == nil || job.State != store.StateWaiting {
		return errors.New("there is no disc waiting")
	}

	job.Plan.Package = &pkg
	job.Plan.Edition = pkg.Edition
	r.Reestimate(job)
	r.save(job)
	return nil
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

// convert takes a copied disc from the Master to a Delivery, following the
// job's Plan. It is also where an interrupted job picks up again.
func (r *Runner) convert(ctx context.Context, job *Job, title meta.Title) error {
	// OCR belongs here. Until it exists, the delivery carries no subtitles and
	// says so plainly rather than quietly omitting them.
	job.Stage = store.StageOCR
	if r.hasSelectedSubtitles(job) {
		job.Log.Printf(store.StageOCR,
			"This disc has subtitles, but ARFABIT cannot read them into text yet, so the movie will not have any. They are still in the master copy.")
	}

	// Wait for a turn at the processor. Ripping is over by now and the drive
	// is free, so the next disc can be going in while this one waits.
	job.File = title.VideoName(job.Plan.Edition)

	if r.Slots != nil {
		err := r.Slots.Take(ctx, Ticket{ID: job.ID, Waiting: func() {
			job.Stage = store.StageQueued
			job.Progress = Progress{Since: time.Now(), Operation: "Waiting for a turn"}
			job.Log.Printf(store.StageQueued,
				"Waiting to convert: something else is using the processor. The disc is already copied, so nothing is holding up the drive.")
			r.save(job)
		}})
		if err != nil {
			return r.stop(job, "Stopped while waiting to convert.", "")
		}
		defer r.Slots.Give()
	}

	job.Stage = store.StagePackage
	packageStart := time.Now()
	job.Progress = Progress{Since: packageStart, Operation: "Making the movie file"}
	job.Log.Printf(store.StagePackage,
		"Making the movie file for your Apple TV. Converting the picture is slow; there is nothing to do but wait.")
	r.save(job)

	delivery, err := r.packageMaster(ctx, job, title)
	var replace *ReplaceError
	if errors.As(err, &replace) {
		return r.stop(job, fmt.Sprintf(
			"%s is already in your library, so ARFABIT stopped rather than replace it. Nothing was removed, and the master is kept. Give this one a different edition, or move that file, and start it again.",
			filepath.Base(replace.Path)), replace.Path)
	}
	if err != nil {
		return r.stop(job, "ARFABIT did not finish making the movie file.", detailOf(err))
	}
	job.Delivery = delivery

	job.Stage = store.StageDeliver
	if err := r.deliver(job, title, job.Plan.Edition); err != nil {
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
	out := filepath.Join(outDir, title.VideoName(job.Plan.Edition))
	if err := refuseToReplace(out); err != nil {
		return "", err
	}

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
	var writeRate rateTracker

	err = ffmpeg.Run(ctx, args, ffmpeg.RunOptions{
		Duration: time.Duration(info.Duration * float64(time.Second)),
		OnProgress: func(p ffmpeg.Progress) {
			job.Progress = Progress{
				Percent:   p.Percent(),
				Operation: "Making the movie file",
				Remaining: humanDuration(p.Remaining().Round(time.Minute)),
				Since:     start,
				Rate:      HumanRate(writeRate.Observe(p.Bytes)),
			}
			r.notify(job)
		},
	})
	if err != nil {
		return "", err
	}

	if written, err := os.Stat(out); err == nil && !job.Plan.VideoCopy {
		r.Calibration.ObserveEncode(job.Plan, video.Width, video.Height, written.Size(),
			time.Duration(info.Duration*float64(time.Second)), time.Since(start))
	}

	return out, nil
}

// audioTracks turns the Plan's audio choices into encoder tracks.
//
// The Plan records the disc's own stream numbers, which are not the master's:
// MakeMKV keeps only some streams and renumbers what it keeps. So each planned
// track is matched back to a real stream in the master by what it is, and the
// decision about copying is taken from the master rather than from the Plan —
// the master is what gets muxed, and trusting the Plan here once copied a track
// that could not play as it was.
func (r *Runner) audioTracks(job *Job, info *ffmpeg.MediaInfo) []ffmpeg.AudioTrack {
	sources := info.StreamsOfKind("audio")
	if len(sources) == 0 {
		return nil
	}

	used := map[int]bool{}
	var tracks []ffmpeg.AudioTrack

	for _, planned := range job.Plan.Audio {
		if !planned.Selected {
			continue
		}

		// A downmix is made from a track that is already being kept, so it
		// looks among all the streams rather than the unclaimed ones.
		claimed := used
		if planned.Stereo {
			claimed = nil
		}

		source := matchStream(sources, planned, claimed)
		if source == nil {
			job.Log.Printf(store.StagePackage,
				"The %s track is not in the copy, so it has been left out.", planned.Label)
			continue
		}
		if !planned.Stereo {
			// A downmix shares its source with the track it came from; every
			// other track takes one of its own.
			used[source.Index] = true
		}

		// Copying is decided from what the master actually holds. A track
		// that does not play directly is converted whatever the Plan said.
		canCopy := ffmpeg.CanCopyAudio(source.Codec) && !planned.Stereo
		if planned.Copy && !canCopy {
			job.Log.Printf(store.StagePackage,
				"The %s track is %s, which does not play directly, so it is being converted.",
				planned.Label, source.Codec)
		}

		track := ffmpeg.AudioTrack{
			SourceIndex: source.Index,
			Copy:        canCopy && planned.Copy,
			Codec:       planned.Codec,
			Bitrate:     planned.Bitrate,
			Lang:        planned.Lang,
			Title:       shortTrackName(planned),
			Default:     len(tracks) == 0,
		}

		if !track.Copy {
			if planned.Stereo || planned.Channels <= 2 {
				track.Channels = 2
			}
			if track.Codec == "" || track.Codec == source.Codec {
				// Nothing usable was planned, so fall back to something that
				// certainly plays: FLAC for lossless sound, so nothing is lost,
				// and AAC otherwise.
				track.Codec = "aac"
				if streamLossless(*source) && track.Channels != 2 {
					track.Codec = "flac"
				}
			}
		}

		tracks = append(tracks, track)
	}

	return tracks
}

// matchStream finds the stream in the master that a planned track refers to.
//
// Language and channel count together identify a track well enough in
// practice; the codec breaks ties between, say, the DTS and Dolby versions of
// the same mix. Streams already claimed are skipped so that two planned tracks
// cannot both resolve to the same one, which is exactly what went wrong before.
func matchStream(sources []ffmpeg.Stream, planned store.PlannedAudio, used map[int]bool) *ffmpeg.Stream {
	best := -1
	bestScore := 0

	for i := range sources {
		s := &sources[i]
		if used[s.Index] {
			continue
		}

		score := 0
		if strings.EqualFold(s.Lang, planned.Lang) {
			score += 4
		}
		if s.Channels == planned.Channels {
			score += 3
		} else if planned.Channels > 2 && s.Channels > 2 {
			// Close enough: surround of one width stands in for another
			// rather than the track being dropped altogether.
			score++
		}
		if strings.EqualFold(s.Codec, planned.SourceCodec) {
			score += 2
		}
		if score == 0 {
			continue
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}

	// A track must at least be in the right language, or it is not the track.
	if best < 0 || bestScore < 4 {
		return nil
	}
	return &sources[best]
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

package store

import (
	"fmt"
	"time"

	"github.com/arfabit/arfabit/internal/meta"
)

// Stage is one step of the pipeline. The names match the UI and the logs
// exactly, with no synonyms (§2).
type Stage string

const (
	StageScan Stage = "SCAN"
	StagePlan Stage = "PLAN"
	StageRip  Stage = "RIP"
	StageOCR  Stage = "OCR"
	// StageQueued is waiting for a turn at the processor. It is a stage of its
	// own because "waiting" and "working" look identical otherwise, and a
	// person watching deserves to know which.
	StageQueued Stage = "QUEUED"

	// StageLab is rendering test clips, which is work of the same kind as
	// packaging and queues alongside it.
	StageLab Stage = "LAB"

	StagePackage Stage = "PACKAGE"
	StageDeliver Stage = "DELIVER"
	StageEject   Stage = "EJECT"
)

// Stages in pipeline order.
//
// Ejecting comes straight after the rip rather than at the end: once the copy
// exists the disc has nothing left to give, and everything after it happens on
// the copy. Holding the disc through a two-hour encode would be keeping it for
// no reason.
var Stages = []Stage{StageScan, StagePlan, StageRip, StageEject, StageOCR, StageQueued, StagePackage, StageDeliver}

// State is where a job has got to.
type State string

const (
	// StateWaiting means the Plan is ready and the user has not started it.
	StateWaiting State = "waiting"

	StateRunning State = "running"
	StateDone    State = "done"

	// StateStopped covers both a job the user stopped and one that did not
	// finish. Neither is described as a failure (§15).
	StateStopped State = "stopped"
)

// Kind says what a job is. Everything in the queue is one of these, so that
// one list can show everything the machine is doing.
type Kind string

const (
	// KindDisc is a disc on its way to becoming a film.
	KindDisc Kind = "disc"

	// KindLab is a set of test clips rendered from a copy.
	KindLab Kind = "lab"

	// KindConvert is a copy being turned into a film, with no disc involved.
	KindConvert Kind = "convert"
)

// Job is one piece of work: a disc on its way through the pipeline, or a set
// of test clips.
type Job struct {
	ID   string `json:"id"`
	Node string `json:"node"`
	Kind Kind   `json:"kind"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`

	State State `json:"state"`
	Stage Stage `json:"stage"`

	// Disc is what the scan found.
	DiscName  string `json:"disc_name"`
	DiscLabel string `json:"disc_label"`
	DiscKind  string `json:"disc_kind"`
	Drive     string `json:"drive"`

	// DriveName is the drive's own name, which stays the same from disc to
	// disc. The device path in Drive does not: the system numbers a disc as
	// it appears, and an empty drive has no path at all.
	DriveName string `json:"drive_name,omitempty"`

	// Title is the confirmed name, once chosen.
	Title string `json:"title"`
	Year  int    `json:"year"`

	// Matches are what the offline film list suggested, best first. The Plan
	// shows them so the user can pick a different one.
	Matches []meta.Match `json:"matches,omitempty"`

	// Plan records what was decided, in full, so a job can be understood
	// long after it ran.
	Plan *Plan `json:"plan,omitempty"`

	// Transcode records what a Transcode job was asked to make. Disc jobs
	// have none.
	Transcode *Transcode `json:"transcode,omitempty"`

	// Package is what a package job makes from its Master: the line items,
	// and the files to make them into.
	Package *Package `json:"package,omitempty"`

	// From names the rip whose master this job transcodes, when the two were
	// planned together. The transcode waits for it, and does not start if the
	// rip does not finish.
	From string `json:"from,omitempty"`

	// Files produced, in the order they were made.
	//
	// Made lists every file a package made: its film in the library, or
	// its clip.
	Made     []string `json:"made,omitempty"`
	Master   string   `json:"master,omitempty"`
	Delivery string   `json:"delivery,omitempty"`
	Sidecars []string `json:"sidecars,omitempty"`

	// ReadSpeed is how fast the disc was read through the copy, averaged over
	// each half minute, so the page can draw how it went once it is over.
	ReadSpeed []SpeedSample `json:"read_speed,omitempty"`

	// Note explains the current state in plain language. It is shown to the
	// user, so it never contains jargon or a guessed cause.
	Note string `json:"note,omitempty"`

	// Detail is the raw output behind Note, shown only on request. It is
	// always complete and never a summary (§15).
	Detail string `json:"detail,omitempty"`
}

// Name is what to call the job in a sentence: its confirmed title, or while
// there is none yet, whatever the disc calls itself.
func (j *Job) Name() string {
	for _, name := range []string{j.Title, j.DiscName, j.DiscLabel} {
		if name != "" {
			return name
		}
	}
	return "a disc"
}

// SoundOutcome is what one of a blueprint's sound choices found in one
// language.
type SoundOutcome struct {
	Language string `json:"language"`
	Choice   string `json:"choice"`

	// Matched describes the tracks chosen. Empty means nothing fitted.
	Matched []string `json:"matched,omitempty"`
}

// SpeedSample is how fast something went over one stretch of a stage: all
// that was done in the stretch, divided by how long it took.
type SpeedSample struct {
	// Seconds is how far into the stage the stretch ended, counted from the
	// stage's start.
	Seconds int `json:"seconds"`

	MBPerSecond float64 `json:"mb_per_second"`
}

// Plan is what will happen in one job.
type Plan struct {
	// Blueprint names the blueprint the Plan was filled in from, or is empty
	// when it came from the defaults. It is a record of where the settings
	// started, and nothing reads it to decide anything.
	Blueprint string `json:"blueprint"`

	// Edition names the Delivery's version, as {edition-...} in its filename.
	// Blank means none. A blueprint fills it in with its own edition, and the
	// user can change it or clear it.
	Edition string `json:"edition"`

	TitleIndex int `json:"title_index"`

	// MasterName is what MakeMKV said it would call the master, so the page
	// can name the file while it is still being written.
	MasterName string `json:"master_name,omitempty"`

	Duration   string `json:"duration"`
	SourceSize int64  `json:"source_size"`

	VideoCopy   bool   `json:"video_copy"`
	VideoCodec  string `json:"video_codec"`
	CRF         int    `json:"crf,omitempty"`
	Preset      string `json:"preset,omitempty"`
	SourceCodec string `json:"source_codec"`
	Resolution  string `json:"resolution"`
	HDR         bool   `json:"hdr"`

	Audio     []PlannedAudio    `json:"audio"`
	Subtitles []PlannedSubtitle `json:"subtitles"`

	// Tracks are what the disc's title holds, as the Master will hold them:
	// what the master part of the Plan shows, and what its package is
	// planned from.
	Tracks []Track `json:"tracks,omitempty"`

	// Package is the file to make from the Master once the disc is copied,
	// when Convert is on. It is a job of its own, planned here because this
	// is where the disc's contents are known.
	Package *Package `json:"package,omitempty"`

	// Seconds is how long the title runs, and RipTime how long reading it is
	// expected to take, kept so the estimate can follow the package.
	Seconds int           `json:"seconds,omitempty"`
	RipTime time.Duration `json:"rip_time,omitempty"`

	// Sound is what the blueprint's sound rules found, one line per choice
	// and language, so the Plan can say what each one matched. Empty when the
	// blueprint has no rules.
	Sound []SoundOutcome `json:"sound,omitempty"`

	// SoundNotFound says the blueprint asked for sound and none of it is
	// here, so the tracks are left for the user to choose.
	SoundNotFound bool `json:"sound_not_found,omitempty"`

	// EstimatedSize and EstimatedTime are the numbers shown before starting.
	EstimatedSize int64         `json:"estimated_size"`
	EstimatedTime time.Duration `json:"estimated_time"`

	// Convert says whether to make the Apple TV file after copying the disc.
	//
	// Turning it off stops after the copy, which is the fast way through a
	// stack of discs: the copy is the only part that needs the drive, and
	// converting can be done later from the copy at any time.
	Convert bool `json:"convert"`

	// Obfuscated records that the disc hid its main feature among decoys.
	Obfuscated bool   `json:"obfuscated"`
	Reason     string `json:"reason"`
}

// Transcode is what a Transcode job makes from a Master.
//
// Each Plan is a copy of one blueprint's settings, taken when the job was made.
// A blueprint only helps fill in a Plan: changing or removing it afterwards does
// not reach a job that already has one, and running the job again means
// running these Plans as they are (§8).
type Transcode struct {
	// At and Length say which stretch of the Master. A Length of zero means
	// the whole of it, which makes Deliveries rather than lab clips.
	At     time.Duration `json:"at"`
	Length time.Duration `json:"length"`

	// Plans holds one Plan per blueprint chosen, in the order chosen.
	Plans []*Plan `json:"plans"`
}

// PlannedAudio is one audio track the Plan will produce.
type PlannedAudio struct {
	SourceIndex int    `json:"source_index"`
	Copy        bool   `json:"copy"`
	Codec       string `json:"codec"`
	Layout      string `json:"layout"`
	Channels    int    `json:"channels"`
	Lang        string `json:"lang"`
	Bitrate     string `json:"bitrate,omitempty"`
	Label       string `json:"label"`
	Selected    bool   `json:"selected"`

	// Source says what the track is on the disc, and nothing about what
	// becomes of it: that is the master's business, not the transcode's.
	Source string `json:"source,omitempty"`

	// SourceCodec and SourceLabel record what was on the disc, so the Plan can
	// say what a track was as well as what it becomes.
	SourceCodec string `json:"source_codec"`
	SourceLabel string `json:"source_label,omitempty"`

	// Lossless marks audio carried bit for bit from the studio, which makes it
	// the better source for a downmix even though it cannot be played as-is.
	Lossless bool `json:"lossless,omitempty"`

	// Stereo marks a downmix ARFABIT adds itself rather than a track that
	// exists on the disc.
	Stereo bool `json:"stereo,omitempty"`
}

// PlannedSubtitle is one subtitle track the Plan will produce.
type PlannedSubtitle struct {
	SourceIndex int    `json:"source_index"`
	Lang        string `json:"lang"`
	Forced      bool   `json:"forced"`
	Label       string `json:"label"`
	Selected    bool   `json:"selected"`
}

// NewJob starts a job record.
func NewJob(id string) *Job {
	return &Job{
		ID:      id,
		Kind:    KindDisc,
		Started: time.Now(),
		Updated: time.Now(),
		State:   StateRunning,
		Stage:   StageScan,
	}
}

// NewLabJob starts a job for a set of test clips.
func NewLabJob(id, film string) *Job {
	return &Job{
		ID:      id,
		Kind:    KindLab,
		Title:   film,
		Started: time.Now(),
		Updated: time.Now(),
		State:   StateRunning,
		Stage:   StageLab,
	}
}

// NewJobID makes a readable, sortable identifier.
//
// Readable because these are filenames a person may look at, and sortable so a
// directory listing is in time order.
func NewJobID(now time.Time, label string) string {
	slug := slugify(label)
	if slug == "" {
		slug = "disc"
	}
	return fmt.Sprintf("%s-%s", now.Format("2006-01-02-150405"), slug)
}

func slugify(s string) string {
	var out []rune
	lastDash := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
			lastDash = false
		default:
			if !lastDash && len(out) > 0 {
				out = append(out, '-')
				lastDash = true
			}
		}
		if len(out) >= 40 {
			break
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// LibraryEntry is one line of the shared library index.
type LibraryEntry struct {
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	Edition   string    `json:"edition"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Node      string    `json:"node"`
	JobID     string    `json:"job_id"`
	Delivered time.Time `json:"delivered"`
}

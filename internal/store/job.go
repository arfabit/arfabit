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
	StageScan    Stage = "SCAN"
	StagePlan    Stage = "PLAN"
	StageRip     Stage = "RIP"
	StageOCR     Stage = "OCR"
	StagePackage Stage = "PACKAGE"
	StageDeliver Stage = "DELIVER"
	StageEject   Stage = "EJECT"
)

// Stages in pipeline order.
var Stages = []Stage{StageScan, StagePlan, StageRip, StageOCR, StagePackage, StageDeliver, StageEject}

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

// Job is one disc's trip through the pipeline.
type Job struct {
	ID   string `json:"id"`
	Node string `json:"node"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`

	State State `json:"state"`
	Stage Stage `json:"stage"`

	// Disc is what the scan found.
	DiscName  string `json:"disc_name"`
	DiscLabel string `json:"disc_label"`
	DiscKind  string `json:"disc_kind"`
	Drive     string `json:"drive"`

	// Title is the confirmed name, once chosen.
	Title string `json:"title"`
	Year  int    `json:"year"`

	// Matches are what the offline film list suggested, best first. The Plan
	// shows them so the user can pick a different one.
	Matches []meta.Match `json:"matches,omitempty"`

	// Plan records what was decided, in full, so a job can be understood
	// long after it ran.
	Plan *Plan `json:"plan,omitempty"`

	// Files produced, in the order they were made.
	Master   string   `json:"master,omitempty"`
	Delivery string   `json:"delivery,omitempty"`
	Sidecars []string `json:"sidecars,omitempty"`

	// Note explains the current state in plain language. It is shown to the
	// user, so it never contains jargon or a guessed cause.
	Note string `json:"note,omitempty"`

	// Detail is the raw output behind Note, shown only on request. It is
	// always complete and never a summary (§15).
	Detail string `json:"detail,omitempty"`
}

// Plan is what will happen to this disc.
type Plan struct {
	Profile string `json:"profile"`

	TitleIndex int    `json:"title_index"`
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

	// EstimatedSize and EstimatedTime are the numbers shown before starting.
	EstimatedSize int64         `json:"estimated_size"`
	EstimatedTime time.Duration `json:"estimated_time"`

	// Obfuscated records that the disc hid its main feature among decoys.
	Obfuscated bool   `json:"obfuscated"`
	Reason     string `json:"reason"`
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

	// SourceCodec and SourceLabel record what was on the disc, so the Plan can
	// say what a track was as well as what it becomes.
	SourceCodec string `json:"source_codec"`
	SourceLabel string `json:"source_label,omitempty"`

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
		Started: time.Now(),
		Updated: time.Now(),
		State:   StateRunning,
		Stage:   StageScan,
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

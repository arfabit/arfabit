package pipeline

import (
	"fmt"
	"math"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/store"
)

// Calibration is what this machine has learned about its own speed.
//
// Rip speed belongs to the drive and encode speed to the processor, so they
// are keyed separately: two drives of different speeds on one machine converge
// independently (§12).
type Calibration struct {
	Drives map[string]*DriveStats  `json:"drives"`
	Encode map[string]*EncodeStats `json:"encode"`
}

// DriveStats is one drive's observed read speed, in megabytes per second.
type DriveStats struct {
	MBPerSecond map[string]float64 `json:"mb_per_second"`
	Samples     map[string]int     `json:"samples"`
}

// EncodeStats is this machine's observed encoding speed and efficiency at one
// preset, height and quality.
type EncodeStats struct {
	Preset string `json:"preset"`
	Height int    `json:"height"`
	CRF    int    `json:"crf"`

	// FPS is frames encoded per second.
	FPS float64 `json:"fps"`

	// BitsPerPixel is how many bits the encoder spent on the picture per
	// pixel at this setting, which is what predicts the finished size. Sound
	// is left out: it is counted separately, from the Plan.
	BitsPerPixel float64 `json:"bits_per_pixel"`

	Samples int `json:"samples"`
}

// Conservative starting points, used until real observations arrive. They are
// deliberately pessimistic: an estimate that improves is better than one that
// slips.
const (
	seedRipMBPerSecond = 15.0
	seedEncodeFPS      = 4.0
	seedBitsPerPixel   = 0.075
	seedCRF            = 20 // the quality seedBitsPerPixel stands for
	seedFrameRate      = 24.0
	recentWeight       = 0.3 // how much a new sample moves the average
	confidentAfter     = 5   // samples before an estimate stops being a range
)

// NewCalibration returns an empty calibration.
func NewCalibration() *Calibration {
	return &Calibration{
		Drives: map[string]*DriveStats{},
		Encode: map[string]*EncodeStats{},
	}
}

// Estimate is what the Plan shows before anything starts.
type Estimate struct {
	Size int64
	Time time.Duration

	// Confident reports whether enough jobs have run for this to be a single
	// number rather than a range. The UI says so honestly either way.
	Confident bool
}

// Describe renders the estimate the way the UI states it.
func (e Estimate) Describe() string {
	if e.Time <= 0 {
		return "unknown"
	}
	rounded := e.Time.Round(time.Minute)
	if e.Confident {
		return fmt.Sprintf("about %s", humanDuration(rounded))
	}
	// Without enough samples, a range is the honest answer.
	low := time.Duration(float64(rounded) * 0.6).Round(time.Minute)
	high := time.Duration(float64(rounded) * 1.6).Round(time.Minute)
	return fmt.Sprintf("roughly %s to %s", humanDuration(low), humanDuration(high))
}

// DriveKey is what a drive's read speed is kept under: its name, which stays
// the same from disc to disc. A device path does not — the system numbers a
// disc as it appears, and an empty drive has none — so it is used only when
// there is no name, as for jobs from before names were kept.
func DriveKey(name, device string) string {
	if name != "" {
		return name
	}
	return device
}

// EstimateRip predicts how long reading a title off the disc will take.
func (c *Calibration) EstimateRip(device string, kind disc.Kind, sizeBytes int64) Estimate {
	speed, samples := seedRipMBPerSecond, 0
	if d := c.Drives[device]; d != nil {
		if v, ok := d.MBPerSecond[string(kind)]; ok && v > 0 {
			speed = v
			samples = d.Samples[string(kind)]
		}
	}

	seconds := float64(sizeBytes) / (speed * 1_000_000)
	return Estimate{
		Size:      sizeBytes,
		Time:      time.Duration(seconds) * time.Second,
		Confident: samples >= confidentAfter,
	}
}

// EstimatePackage predicts the finished file's size and how long making it
// takes.
func (c *Calibration) EstimatePackage(plan *store.Plan, duration time.Duration) Estimate {
	// A copy is quick and its size is already known exactly.
	if plan.VideoCopy {
		return Estimate{
			Size:      plan.SourceSize,
			Time:      duration / 20,
			Confident: true,
		}
	}

	width, height := parseResolution(plan.Resolution)
	fps, bpp, samples := c.encodeStats(plan.Preset, height, plan.CRF)

	// Size is bits per pixel across every frame, plus the audio.
	frames := duration.Seconds() * seedFrameRate
	videoBits := bpp * float64(width*height) * frames
	size := int64(videoBits/8) + audioBytes(plan, duration)

	// Time is frames divided by how fast this machine encodes them.
	seconds := frames / math.Max(fps, 0.1)

	return Estimate{
		Size:      size,
		Time:      time.Duration(seconds) * time.Second,
		Confident: samples >= confidentAfter,
	}
}

// audioBytes is the size of the planned audio tracks.
func audioBytes(plan *store.Plan, duration time.Duration) int64 {
	var total int64
	for _, a := range plan.Audio {
		if !a.Selected {
			continue
		}
		bitrate := 256_000.0 // the stereo fallback
		if a.Copy {
			// A copied Dolby track is typically 640 kbps.
			bitrate = 640_000
		}
		total += int64(bitrate * duration.Seconds() / 8)
	}
	return total
}

// ObserveRip folds a completed rip into the calibration.
func (c *Calibration) ObserveRip(device string, kind disc.Kind, sizeBytes int64, elapsed time.Duration) {
	if elapsed <= 0 || sizeBytes <= 0 {
		return
	}

	d := c.Drives[device]
	if d == nil {
		d = &DriveStats{MBPerSecond: map[string]float64{}, Samples: map[string]int{}}
		c.Drives[device] = d
	}

	observed := float64(sizeBytes) / 1_000_000 / elapsed.Seconds()
	d.MBPerSecond[string(kind)] = blend(d.MBPerSecond[string(kind)], observed)
	d.Samples[string(kind)]++
}

// encodeStats is the speed and efficiency to expect at a setting.
//
// An exact match is used when there is one. Otherwise the nearest quality
// observed at the same preset and height stands in, adjusted for the
// difference; failing that, the seed does, adjusted the same way. Either way
// the sample count is zero, so the estimate reads as a range.
//
// Without the adjustment, two blueprints differing only in quality would be
// estimated at the same size, which makes choosing between them pointless.
func (c *Calibration) encodeStats(preset string, height, crf int) (fps, bpp float64, samples int) {
	if e := c.Encode[encodeKey(preset, height, crf)]; e != nil && e.Samples > 0 {
		return e.FPS, e.BitsPerPixel, e.Samples
	}

	var nearest *EncodeStats
	for _, e := range c.Encode {
		if e.Preset != preset || e.Height != height || e.Samples == 0 {
			continue
		}
		if nearest == nil || abs(e.CRF-crf) < abs(nearest.CRF-crf) {
			nearest = e
		}
	}
	if nearest != nil {
		return nearest.FPS, nearest.BitsPerPixel * crfScale(nearest.CRF, crf), 0
	}

	return seedEncodeFPS, seedBitsPerPixel * crfScale(seedCRF, crf), 0
}

// crfStepsPerHalving is how many steps of CRF halve the bitrate, which is how
// x265's quality scale is built: each six steps roughly halves it. Only used to
// carry an observation across to a quality not yet seen, so it need only be
// near.
const crfStepsPerHalving = 6.0

// crfScale is how much the bitrate changes going from one CRF to another.
func crfScale(from, to int) float64 {
	return math.Pow(2, float64(from-to)/crfStepsPerHalving)
}

// ObserveEncode folds a completed encode into the calibration.
//
// The size is the whole file. The Plan's sound is taken off it first, because
// sound is estimated separately and would otherwise be counted twice.
func (c *Calibration) ObserveEncode(plan *store.Plan, width, height int, sizeBytes int64, duration, elapsed time.Duration) {
	if elapsed <= 0 || duration <= 0 || sizeBytes <= 0 || width <= 0 || height <= 0 {
		return
	}

	videoBytes := sizeBytes - audioBytes(plan, duration)
	if videoBytes <= 0 {
		return
	}

	key := encodeKey(plan.Preset, height, plan.CRF)
	e := c.Encode[key]
	if e == nil {
		e = &EncodeStats{Preset: plan.Preset, Height: height, CRF: plan.CRF}
		c.Encode[key] = e
	}

	frames := duration.Seconds() * seedFrameRate

	e.FPS = blend(e.FPS, frames/elapsed.Seconds())
	e.BitsPerPixel = blend(e.BitsPerPixel, float64(videoBytes)*8/(float64(width*height)*frames))
	e.Samples++
}

// blend moves an average toward a new observation, weighted so recent jobs
// matter more without one odd disc throwing everything off.
func blend(current, observed float64) float64 {
	if current <= 0 {
		return observed
	}
	return current*(1-recentWeight) + observed*recentWeight
}

func encodeKey(preset string, height, crf int) string {
	return fmt.Sprintf("x265/%s/%dp/crf%d", preset, height, crf)
}

func parseResolution(s string) (w, h int) {
	if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil {
		return 1920, 1080
	}
	return w, h
}

// humanDuration renders a duration in the units a person would use.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%d hours", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

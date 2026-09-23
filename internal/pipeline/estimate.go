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

// EncodeStats is this machine's observed encoding speed and efficiency.
type EncodeStats struct {
	// FPS is frames encoded per second.
	FPS float64 `json:"fps"`

	// BitsPerPixel is how many bits the encoder spent per pixel at this
	// setting, which is what predicts the finished size.
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
	key := encodeKey(plan.Preset, height)

	fps, bpp, samples := seedEncodeFPS, seedBitsPerPixel, 0
	if e := c.Encode[key]; e != nil && e.Samples > 0 {
		fps, bpp, samples = e.FPS, e.BitsPerPixel, e.Samples
	}

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

// ObserveEncode folds a completed encode into the calibration.
func (c *Calibration) ObserveEncode(preset string, height int, sizeBytes int64, duration, elapsed time.Duration) {
	if elapsed <= 0 || duration <= 0 || sizeBytes <= 0 {
		return
	}

	key := encodeKey(preset, height)
	e := c.Encode[key]
	if e == nil {
		e = &EncodeStats{}
		c.Encode[key] = e
	}

	frames := duration.Seconds() * seedFrameRate
	width := widthFor(height)

	e.FPS = blend(e.FPS, frames/elapsed.Seconds())
	e.BitsPerPixel = blend(e.BitsPerPixel, float64(sizeBytes)*8/(float64(width*height)*frames))
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

func encodeKey(preset string, height int) string {
	return fmt.Sprintf("x265/%s/%dp", preset, height)
}

// widthFor guesses a width from a height for the common shapes, used only to
// turn bits-per-pixel into a size.
func widthFor(height int) int {
	switch {
	case height >= 2000:
		return 3840
	case height >= 1000:
		return 1920
	case height >= 700:
		return 1280
	default:
		return 720
	}
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

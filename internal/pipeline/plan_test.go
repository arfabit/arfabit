package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/store"
)

// blurayDisc mirrors a real Blu-ray: H.264 video, a lossless track Apple TV
// cannot decode, a Dolby track it can, and English subtitles full and forced.
func blurayDisc() (*disc.Disc, disc.Selection) {
	title := disc.Title{
		Index:     0,
		Duration:  109 * time.Minute,
		SizeBytes: 33_000_000_000,
		Streams: []disc.Stream{
			{Index: 0, Kind: disc.StreamVideo, CodecID: "V_MPEG4/ISO/AVC", CodecLong: "Mpeg4 AVC High@L4.1", Width: 1920, Height: 1080},
			{Index: 1, Kind: disc.StreamAudio, CodecID: "A_TRUEHD", Channels: 8, Layout: "7.1", Lang: "eng", Summary: "TrueHD Surround 7.1 English"},
			{Index: 2, Kind: disc.StreamAudio, CodecID: "A_AC3", Channels: 6, Layout: "5.1", Lang: "eng", Summary: "DD Surround 5.1 English"},
			{Index: 3, Kind: disc.StreamAudio, CodecID: "A_DTS", Channels: 6, Layout: "5.1", Lang: "fra", Summary: "DTS Surround 5.1 French"},
			{Index: 4, Kind: disc.StreamSubtitle, Lang: "eng", Summary: "PGS English"},
			{Index: 5, Kind: disc.StreamSubtitle, Lang: "eng", Forced: true, Summary: "PGS English (forced only)"},
			{Index: 6, Kind: disc.StreamSubtitle, Lang: "fra", Summary: "PGS French"},
		},
	}
	d := &disc.Disc{Kind: disc.KindBluray, Name: "Crime 101", Titles: []disc.Title{title}}
	return d, disc.Selection{Feature: 0, Reason: "The longest title is selected as the movie."}
}

func TestBuildPlanVideo(t *testing.T) {
	d, sel := blurayDisc()
	plan, err := BuildPlan(d, sel, config.Defaults().Profile)
	if err != nil {
		t.Fatal(err)
	}

	// A Blu-ray is H.264, and day one encodes it to HEVC.
	if plan.VideoCopy {
		t.Error("VideoCopy = true for an H.264 Blu-ray")
	}
	if plan.VideoCodec != "hevc" || plan.CRF != 20 || plan.Preset != "slow" {
		t.Errorf("video plan = %s crf=%d preset=%s", plan.VideoCodec, plan.CRF, plan.Preset)
	}
	if plan.Resolution != "1920x1080" {
		t.Errorf("Resolution = %q", plan.Resolution)
	}
}

// A UHD disc is already HEVC, so a copy is offered and chosen by default:
// re-encoding it is lossy-to-lossy.
func TestBuildPlanUHDCopies(t *testing.T) {
	d, sel := blurayDisc()
	d.Kind = disc.KindUHD
	d.Titles[0].Streams[0].CodecID = "V_MPEGH/ISO/HEVC"
	d.Titles[0].Streams[0].Width, d.Titles[0].Streams[0].Height = 3840, 2160

	plan, err := BuildPlan(d, sel, config.Defaults().Profile)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.VideoCopy {
		t.Error("VideoCopy = false for a UHD disc that is already HEVC")
	}
}

func TestBuildPlanUHDCopyCanBeTurnedOff(t *testing.T) {
	d, sel := blurayDisc()
	d.Kind = disc.KindUHD
	d.Titles[0].Streams[0].CodecID = "V_MPEGH/ISO/HEVC"

	profile := config.Defaults().Profile
	profile.AllowUHDCopy = false

	plan, err := BuildPlan(d, sel, profile)
	if err != nil {
		t.Fatal(err)
	}
	if plan.VideoCopy {
		t.Error("VideoCopy = true despite allow_uhd_copy being off")
	}
}

// The copy rule: a Dolby track passes through untouched, while TrueHD and DTS
// must be encoded because Apple TV cannot decode them.
func TestBuildPlanAudioCopyRule(t *testing.T) {
	d, sel := blurayDisc()
	plan, err := BuildPlan(d, sel, config.Defaults().Profile)
	if err != nil {
		t.Fatal(err)
	}

	// The added stereo downmix shares the primary track's source index, so
	// it is kept separate here.
	byIndex := map[int]store.PlannedAudio{}
	var stereo *store.PlannedAudio
	for i, a := range plan.Audio {
		if a.Stereo {
			stereo = &plan.Audio[i]
			continue
		}
		byIndex[a.SourceIndex] = a
	}

	if stereo == nil {
		t.Fatal("no stereo fallback was planned")
	}
	if stereo.Copy || stereo.Codec != "aac" || !stereo.Selected {
		t.Errorf("stereo fallback = %+v, want a selected AAC downmix", *stereo)
	}

	if byIndex[1].Copy {
		t.Error("TrueHD marked as copyable; Apple TV cannot decode it")
	}
	if !byIndex[2].Copy {
		t.Error("AC-3 not marked as copyable; Apple TV decodes it natively")
	}
	if byIndex[3].Copy {
		t.Error("DTS marked as copyable")
	}

	// The copyable English track is the one selected: surround for free,
	// even though the TrueHD track has more channels.
	if !byIndex[2].Selected {
		t.Error("the copyable Dolby 5.1 track was not selected")
	}
	if byIndex[1].Selected {
		t.Error("the TrueHD track was selected; it would have to be re-encoded")
	}

	// Multichannel first: Apple TV picks the first track it understands.
	if plan.Audio[len(plan.Audio)-1].Stereo != true {
		t.Error("the stereo fallback is not listed last")
	}
}

// Forced subtitles default to on; other languages are left unselected.
func TestBuildPlanSubtitles(t *testing.T) {
	d, sel := blurayDisc()
	plan, err := BuildPlan(d, sel, config.Defaults().Profile)
	if err != nil {
		t.Fatal(err)
	}

	byIndex := map[int]store.PlannedSubtitle{}
	for _, s := range plan.Subtitles {
		byIndex[s.SourceIndex] = s
	}

	if !byIndex[5].Selected || !byIndex[5].Forced {
		t.Error("the English forced track should be selected by default")
	}
	if !byIndex[4].Selected {
		t.Error("the full English track should be selected by default")
	}
	if byIndex[6].Selected {
		t.Error("French subtitles were selected despite only English being wanted")
	}
}

// Obfuscation reaches the Plan so the user is told, rather than it being
// resolved silently.
func TestBuildPlanCarriesObfuscation(t *testing.T) {
	d, sel := blurayDisc()
	sel.Obfuscated = true
	sel.Reason = "This disc lists 3 titles of exactly the same length..."

	plan, err := BuildPlan(d, sel, config.Defaults().Profile)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Obfuscated || plan.Reason == "" {
		t.Error("obfuscation did not reach the Plan")
	}
}

func TestBuildPlanRejectsTitleWithNoPicture(t *testing.T) {
	d := &disc.Disc{Kind: disc.KindBluray, Titles: []disc.Title{{Index: 0, Duration: time.Hour}}}
	if _, err := BuildPlan(d, disc.Selection{Feature: 0}, config.Defaults().Profile); err == nil {
		t.Error("a title with no video stream was accepted")
	}
}

func TestEstimateImprovesWithSamples(t *testing.T) {
	c := NewCalibration()
	plan := &store.Plan{Resolution: "1920x1080", Preset: "slow", Audio: []store.PlannedAudio{{Selected: true, Copy: true}}}

	first := c.EstimatePackage(plan, 109*time.Minute)
	if first.Confident {
		t.Error("an estimate with no observations claimed confidence")
	}
	if !strings.Contains(first.Describe(), "roughly") {
		t.Errorf("an unconfident estimate should read as a range: %q", first.Describe())
	}

	for i := 0; i < confidentAfter; i++ {
		c.ObserveEncode("slow", 1080, 9_000_000_000, 109*time.Minute, 4*time.Hour)
	}

	later := c.EstimatePackage(plan, 109*time.Minute)
	if !later.Confident {
		t.Error("still unconfident after enough samples")
	}
	if !strings.Contains(later.Describe(), "about") {
		t.Errorf("a confident estimate should read as a single figure: %q", later.Describe())
	}
}

// Rip speed belongs to the drive, so two drives converge independently.
func TestCalibrationKeepsDrivesSeparate(t *testing.T) {
	c := NewCalibration()
	c.ObserveRip("/dev/disk4", disc.KindBluray, 30_000_000_000, 30*time.Minute)
	c.ObserveRip("/dev/disk9", disc.KindBluray, 30_000_000_000, 60*time.Minute)

	fast := c.EstimateRip("/dev/disk4", disc.KindBluray, 30_000_000_000)
	slow := c.EstimateRip("/dev/disk9", disc.KindBluray, 30_000_000_000)

	if !(fast.Time < slow.Time) {
		t.Errorf("the faster drive did not produce a shorter estimate: %v vs %v", fast.Time, slow.Time)
	}
}

// A copy's size is already known exactly, so it needs no guessing.
func TestEstimateCopyIsExact(t *testing.T) {
	c := NewCalibration()
	plan := &store.Plan{VideoCopy: true, SourceSize: 54_000_000_000}

	est := c.EstimatePackage(plan, 2*time.Hour)
	if est.Size != plan.SourceSize || !est.Confident {
		t.Errorf("a copy should be exact: size=%d confident=%v", est.Size, est.Confident)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{500, "500 bytes"},
		{33_457_569_792, "33.5 GB"},
		{1_500_000, "1.5 MB"},
	}
	for _, tc := range tests {
		if got := HumanBytes(tc.in); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The space message states four numbers and suggests nothing be removed.
func TestSpaceDescribe(t *testing.T) {
	s := Space{
		Needed:  50_000_000_000,
		Free:    10_000_000_000,
		Masters: 400_000_000_000,
		Library: 80_000_000_000,
	}
	got := s.Describe()

	for _, want := range []string{"50.0 GB", "10.0 GB", "400.0 GB", "80.0 GB"} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"delete", "Delete", "remove", "failed"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("message says %q, which ARFABIT never suggests:\n%s", unwanted, got)
		}
	}

	// Nothing is said when there is plenty of room.
	if (Space{Needed: 1, Free: 1_000_000, Fits: true}).Describe() != "" {
		t.Error("a message was produced when there is plenty of room")
	}
}

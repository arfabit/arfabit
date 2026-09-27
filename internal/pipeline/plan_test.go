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
	plan, err := BuildPlan(d, sel, config.Defaults().Plain(), false)
	if err != nil {
		t.Fatal(err)
	}

	// A Blu-ray is H.264, and day one encodes it to HEVC.
	if plan.VideoCopy {
		t.Error("VideoCopy = true for an H.264 Blu-ray")
	}
	if plan.CRF != 20 || plan.Preset != "slow" {
		t.Errorf("video plan = crf=%d preset=%s", plan.CRF, plan.Preset)
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

	plan, err := BuildPlan(d, sel, config.Defaults().Plain(), false)
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

	blueprint := config.Defaults().Plain()
	blueprint.AllowUHDCopy = false

	plan, err := BuildPlan(d, sel, blueprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.VideoCopy {
		t.Error("VideoCopy = true despite allow_uhd_copy being off")
	}
}

// The copy rule: a Dolby track passes through untouched, while TrueHD and DTS
// must be encoded because Apple TV cannot decode them.
// Forced subtitles default to on; other languages are left unselected.
// Obfuscation reaches the Plan so the user is told, rather than it being
// resolved silently.
func TestBuildPlanCarriesObfuscation(t *testing.T) {
	d, sel := blurayDisc()
	sel.Obfuscated = true
	sel.Reason = "This disc lists 3 titles of exactly the same length..."

	plan, err := BuildPlan(d, sel, config.Defaults().Plain(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Obfuscated || plan.Reason == "" {
		t.Error("obfuscation did not reach the Plan")
	}
}

func TestBuildPlanRejectsTitleWithNoPicture(t *testing.T) {
	d := &disc.Disc{Kind: disc.KindBluray, Titles: []disc.Title{{Index: 0, Duration: time.Hour}}}
	if _, err := BuildPlan(d, disc.Selection{Feature: 0}, config.Defaults().Plain(), false); err == nil {
		t.Error("a title with no video stream was accepted")
	}
}

func TestEstimateImprovesWithSamples(t *testing.T) {
	c := NewCalibration()
	plan := &store.Plan{Resolution: "1920x1080", Preset: "slow", CRF: 20, Audio: []store.PlannedAudio{{Selected: true, Copy: true}}}

	first := c.EstimatePackage(plan, 109*time.Minute)
	if first.Confident {
		t.Error("an estimate with no observations claimed confidence")
	}
	if !strings.Contains(first.Describe(), "roughly") {
		t.Errorf("an unconfident estimate should read as a range: %q", first.Describe())
	}

	for i := 0; i < confidentAfter; i++ {
		c.ObserveEncode(plan, 1920, 1080, 9_000_000_000, 109*time.Minute, 4*time.Hour)
	}

	later := c.EstimatePackage(plan, 109*time.Minute)
	if !later.Confident {
		t.Error("still unconfident after enough samples")
	}
	if !strings.Contains(later.Describe(), "about") {
		t.Errorf("a confident estimate should read as a single figure: %q", later.Describe())
	}
}

// A lower CRF is a larger file. Two blueprints differing only in quality must
// not be estimated at the same size, observed or not.
func TestEstimateFollowsQuality(t *testing.T) {
	c := NewCalibration()
	at := func(crf int) *store.Plan {
		return &store.Plan{Resolution: "1920x1080", Preset: "slow", CRF: crf}
	}

	if !(c.EstimatePackage(at(18), 2*time.Hour).Size > c.EstimatePackage(at(24), 2*time.Hour).Size) {
		t.Error("before any observation, CRF 18 was not estimated larger than CRF 24")
	}

	// One quality observed carries across to the others, adjusted.
	c.ObserveEncode(at(20), 1920, 1080, 8_000_000_000, 2*time.Hour, 3*time.Hour)

	observed := c.EstimatePackage(at(20), 2*time.Hour)
	if observed.Size < 7_500_000_000 || observed.Size > 8_500_000_000 {
		t.Errorf("the observed quality came back as %d bytes, want near 8 GB", observed.Size)
	}

	lower := c.EstimatePackage(at(26), 2*time.Hour)
	if !(lower.Size*3 < observed.Size*2) {
		t.Errorf("six steps of CRF higher should be about half the size: %d vs %d", lower.Size, observed.Size)
	}
	if lower.Confident {
		t.Error("a quality never observed claimed confidence")
	}
}

// The sound is estimated separately, so an observation must not count it as
// picture: doing so made every estimate after the first too large.
func TestObservationLeavesOutTheSound(t *testing.T) {
	c := NewCalibration()
	plan := &store.Plan{Resolution: "1920x1080", Preset: "slow", CRF: 20,
		Audio: []store.PlannedAudio{{Selected: true, Copy: true}}}

	c.ObserveEncode(plan, 1920, 1080, 8_000_000_000, 2*time.Hour, 3*time.Hour)

	if got := c.EstimatePackage(plan, 2*time.Hour).Size; got < 7_500_000_000 || got > 8_500_000_000 {
		t.Errorf("the same job estimated at %d bytes, want near the 8 GB it came to", got)
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
		Needed:    50_000_000_000,
		Free:      10_000_000_000,
		Originals: 400_000_000_000,
		Library:   80_000_000_000,
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

// A check that could not run must not stop a job: that would be ARFABIT
// getting in the way over its own shortcoming rather than a real shortage.
func TestSpaceFailsOpen(t *testing.T) {
	space, err := CheckSpace(1_000_000, "/no/such/volume/that/exists")
	if err == nil {
		t.Skip("this system reported free space for a path that does not exist")
	}
	if !space.Fits {
		t.Error("a failed check refused the job")
	}
	if !space.Unknown {
		t.Error("Unknown = false after a failed check")
	}
	if !strings.Contains(space.Describe(), "could not check") {
		t.Errorf("the message does not admit the check failed: %q", space.Describe())
	}
}

// Some discs carry surround only in formats an Apple TV cannot decode. Keeping
// the bit-perfect stereo track is right, but the loss must be said out loud.
// When surround does survive, there is nothing to say.
// Tracks are grouped the way a disc's own menu reads: wanted languages first,
// widest first inside each language.
// Discs often carry two stereo tracks with nothing to tell them apart — one is
// frequently a commentary. ARFABIT cannot know which, so it keeps both rather
// than picking wrongly.
// Labels say what a track is and what becomes of it, in words rather than
// codec identifiers.
// MakeMKV reports layouts like "5.1(side)", which is accurate and unhelpful.
func TestLayoutNames(t *testing.T) {
	tests := []struct {
		channels int
		raw      string
		want     string
	}{
		{8, "7.1", "7.1"},
		{6, "5.1(side)", "5.1"},
		// Numbered like 5.1 and 7.1: two channels and no bass channel.
		{2, "stereo", "2.0"},
		{1, "mono", "1.0"},
	}
	for _, tc := range tests {
		if got := layoutName(tc.channels, tc.raw); got != tc.want {
			t.Errorf("layoutName(%d, %q) = %q, want %q", tc.channels, tc.raw, got, tc.want)
		}
	}
}

// The three encoders differ in how many channels they will write, which was
// measured rather than assumed: AAC writes eight, E-AC-3's ffmpeg encoder
// stops at six and downmixes anything wider without saying so. The codec is
// therefore chosen by how wide the source is.
// Lossy sound is converted only when the blueprint asks for it not to be kept
// as it is. Then the target follows the width, because the encoders differ.
// Stereo is what arrives without choosing anything, and every surround track
// is listed beside it so turning one on is a single click.
// A disc with no stereo track still delivers one, made from its widest.
// A disc's own stereo track is a purpose-made mix, but it is usually Dolby at
// a few hundred kilobits while the surround track beside it is lossless. Both
// routes to stereo are offered, and the disc's own is the one ticked.
// With no lossless track there is nothing better to offer, so nothing is.
// DTS-HD Master Audio shares its codec id with ordinary DTS and is told apart
// only by the long name.
func TestLosslessDetection(t *testing.T) {
	tests := []struct {
		codecID, codecLong string
		want               bool
	}{
		{"A_TRUEHD", "TrueHD Atmos", true},
		{"A_DTS", "DTS-HD Master Audio", true},
		{"A_DTS", "DTS", false},
		{"A_AC3", "Dolby Digital", false},
		{"A_FLAC", "FLAC", true},
	}
	for _, tc := range tests {
		if got := isLossless(tc.codecID, tc.codecLong); got != tc.want {
			t.Errorf("isLossless(%q, %q) = %v, want %v", tc.codecID, tc.codecLong, got, tc.want)
		}
	}
}

// The Plan's edition comes from its blueprint, and is blank from the defaults.
func TestPlanEditionComesFromTheBlueprint(t *testing.T) {
	d, sel := blurayDisc()

	plain, err := BuildPlan(d, sel, config.Defaults().Plain(), false)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Edition != "" || plain.Blueprint != "" {
		t.Errorf("a Plan from the defaults has blueprint %q, edition %q", plain.Blueprint, plain.Edition)
	}

	bp := config.Defaults().Plain()
	bp.Name, bp.Edition = "Small", "Travel"
	small, err := BuildPlan(d, sel, bp, false)
	if err != nil {
		t.Fatal(err)
	}
	if small.Blueprint != "Small" || small.Edition != "Travel" {
		t.Errorf("a Plan from Small has blueprint %q, edition %q", small.Blueprint, small.Edition)
	}
}

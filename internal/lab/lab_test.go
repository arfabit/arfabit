package lab

import (
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
)

func testVideo() *ffmpeg.Stream {
	return &ffmpeg.Stream{Index: 0, Kind: "video", Codec: "h264", Width: 1920, Height: 1080}
}

// Seeking before the input makes ffmpeg jump to the right place instead of
// decoding everything up to it — the difference between a clip taking seconds
// and taking as long as the film.
func TestSeekComesBeforeTheInput(t *testing.T) {
	args := clipArgs(Request{
		Master: "master.mkv",
		At:     45 * time.Minute,
		Length: 30 * time.Second,
	}, Clip{Name: "test", Video: VideoSetting{CRF: 20, Preset: "slow"}}, testVideo())

	joined := strings.Join(args, " ")
	seek := strings.Index(joined, "-ss")
	input := strings.Index(joined, "-i ")

	if seek < 0 || input < 0 {
		t.Fatalf("args are missing a seek or an input: %v", args)
	}
	if seek > input {
		t.Error("the seek comes after the input, so the whole film would be decoded to reach the clip")
	}
	if !strings.Contains(joined, "2700.000") {
		t.Errorf("45 minutes was not passed as seconds: %v", args)
	}
}

func TestClipArgsVideoSettings(t *testing.T) {
	args := strings.Join(clipArgs(Request{Master: "m.mkv", Length: time.Minute},
		Clip{Name: "crf18", Video: VideoSetting{CRF: 18, Preset: "medium"}}, testVideo()), " ")

	for _, want := range []string{"-c:v libx265", "-crf 18", "-preset medium", "-profile:v main10", "-tag:v hvc1"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
}

// A copy is the honest baseline every other setting is judged against.
func TestClipArgsCopyBaseline(t *testing.T) {
	args := strings.Join(clipArgs(Request{Master: "m.mkv", Length: time.Minute},
		Clip{Name: "untouched", Video: VideoSetting{Copy: true}, Audio: AudioSetting{Copy: true}}, testVideo()), " ")

	if !strings.Contains(args, "-c:v copy") || !strings.Contains(args, "-c:a copy") {
		t.Errorf("a baseline clip re-encoded something: %s", args)
	}
	if strings.Contains(args, "libx265") {
		t.Errorf("a copy invoked the encoder: %s", args)
	}
}

// Sound can be judged on its own, with the picture left untouched beneath it.
func TestClipArgsAudioOnly(t *testing.T) {
	args := strings.Join(clipArgs(Request{Master: "m.mkv", Length: 2 * time.Minute},
		Clip{
			Name:  "eac3-768",
			Video: VideoSetting{Copy: true},
			Audio: AudioSetting{Codec: "eac3", Bitrate: "768k", SourceIndex: 2},
		}, testVideo()), " ")

	if !strings.Contains(args, "-c:v copy") {
		t.Errorf("the picture was re-encoded while judging sound: %s", args)
	}
	if !strings.Contains(args, "-c:a eac3") || !strings.Contains(args, "-b:a 768k") {
		t.Errorf("the audio setting did not reach ffmpeg: %s", args)
	}
	if !strings.Contains(args, "-map 0:2") {
		t.Errorf("the chosen audio track was not selected: %s", args)
	}
}

// A lab clip that lost its HDR metadata would look grey beside one that kept
// it, and the comparison would be of the wrong thing entirely.
func TestClipKeepsHDR(t *testing.T) {
	video := testVideo()
	video.ColorInfo = ffmpeg.ColorInfo{Primaries: "bt2020", Transfer: "smpte2084", Space: "bt2020nc"}
	video.HDR = &ffmpeg.HDR{
		GreenX: 13250, GreenY: 34500, BlueX: 7500, BlueY: 3000,
		RedX: 34000, RedY: 16000, WhiteX: 15635, WhiteY: 16450,
		MaxLuminance: 10000000, MinLuminance: 50, MaxCLL: 1000, MaxFALL: 400,
	}

	args := strings.Join(clipArgs(Request{Master: "m.mkv", Length: time.Minute},
		Clip{Name: "uhd", Video: VideoSetting{CRF: 20, Preset: "slow"}}, video), " ")

	for _, want := range []string{"master-display=", "max-cll=1000,400", "hdr10=1", "-color_trc smpte2084"} {
		if !strings.Contains(args, want) {
			t.Errorf("HDR metadata missing %q: %s", want, args)
		}
	}
}

// The whole point of the lab: what a thirty-second clip means for a two-hour
// film, in size and in hours of encoding.
func TestCompareExtrapolatesToTheWholeFilm(t *testing.T) {
	clips := []Clip{
		{Name: "crf20", Size: 100_000_000, Took: time.Minute},
		{Name: "crf24", Size: 50_000_000, Took: 50 * time.Second},
	}

	got := Compare(clips, 30*time.Second, 2*time.Hour)
	if len(got.Clips) != 2 {
		t.Fatalf("got %d rows, want 2", len(got.Clips))
	}

	// Smallest first.
	if got.Clips[0].Clip.Name != "crf24" {
		t.Errorf("rows are not smallest first: %s", got.Clips[0].Clip.Name)
	}

	// Thirty seconds scaled to two hours is 240 times.
	if want := int64(50_000_000 * 240); got.Clips[0].WholeFilm != want {
		t.Errorf("WholeFilm = %d, want %d", got.Clips[0].WholeFilm, want)
	}
	if want := 240 * 50 * time.Second; got.Clips[0].EncodeTime != want {
		t.Errorf("EncodeTime = %v, want %v", got.Clips[0].EncodeTime, want)
	}

	// The best is 100%, and everything else is measured against it.
	if got.Clips[0].SizeShare != 100 {
		t.Errorf("the smallest result is %.1f%%, want 100", got.Clips[0].SizeShare)
	}
	if got.Clips[1].SizeShare != 50 {
		t.Errorf("twice the size should read 50%%, got %.1f%%", got.Clips[1].SizeShare)
	}
}

// A setting that fails must not stop the others being compared: four results
// out of five is still a comparison.
func TestCompareKeepsFailedSettingsInTheTable(t *testing.T) {
	clips := []Clip{
		{Name: "good", Size: 10_000_000, Took: time.Minute},
		{Name: "broken", Problem: "ffmpeg said no"},
	}

	got := Compare(clips, 30*time.Second, time.Hour)
	if len(got.Clips) != 2 {
		t.Fatalf("got %d rows, want both", len(got.Clips))
	}

	var broken *Row
	for i := range got.Clips {
		if got.Clips[i].Clip.Name == "broken" {
			broken = &got.Clips[i]
		}
	}
	if broken == nil {
		t.Fatal("the failed setting vanished from the table")
	}
	if broken.Clip.Problem == "" {
		t.Error("the failed setting lost its explanation")
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName("CRF 20 / slow"); strings.ContainsAny(got, ` /\:`) {
		t.Errorf("safeName left something unsafe: %q", got)
	}
	if got := safeName(""); got != "clip" {
		t.Errorf("an unnamed setting got %q", got)
	}
}

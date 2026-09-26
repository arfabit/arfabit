package ffmpeg

import (
	"strings"
	"testing"
)

func argString(t *testing.T, r EncodeRequest) string {
	t.Helper()
	args, err := r.Args()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(args, " ")
}

// Matroska carries every sound format a disc has, TrueHD included. Whether it
// then plays directly is a note for the user, not a reason to change it.
func TestCanCopyAudio(t *testing.T) {
	for _, codec := range []string{"ac3", "eac3", "aac", "AC3", "dts", "truehd", "flac", "alac", "pcm_s24le"} {
		if !CanCopyAudio(codec) {
			t.Errorf("CanCopyAudio(%q) = false; Matroska carries it", codec)
		}
	}
	if CanCopyAudio("") {
		t.Error("a track of no known format was taken to be copyable")
	}
}

// MPEG-2 and VC-1 must not be copyable: every DVD and some older Blu-rays use
// them, and Apple TV cannot decode either.
func TestCanCopyVideo(t *testing.T) {
	if !CanCopyVideo("hevc") || !CanCopyVideo("h264") {
		t.Error("HEVC and H.264 must be copyable")
	}
	for _, codec := range []string{"mpeg2video", "vc1"} {
		if CanCopyVideo(codec) {
			t.Errorf("CanCopyVideo(%q) = true; Apple TV cannot decode it", codec)
		}
	}
}

func TestEncodeArgsBasics(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:            "master.mkv",
		Output:           "out.mkv",
		VideoSourceIndex: 0,
		Video:            VideoPlan{CRF: 20, Preset: PresetSlow},
	})

	for _, want := range []string{
		"-c:v libx265", "-crf 20", "-preset slow",
		"-profile:v main10", "-pix_fmt yuv420p10le",
		// The master's statistics describe its tracks, not the new ones.
		"-map_metadata -1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\ngot: %s", want, got)
		}
	}
	// Both are MP4's needs, and have no place in Matroska.
	for _, unwanted := range []string{"-tag:v", "-movflags"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("args carry %q, which is for MP4\ngot: %s", unwanted, got)
		}
	}
}

func TestEncodeArgsRejectsUnknownPreset(t *testing.T) {
	_, err := EncodeRequest{
		Input:  "in.mkv",
		Output: "out.mkv",
		Video:  VideoPlan{CRF: 20, Preset: "ultrafast"},
	}.Args()
	if err == nil {
		t.Error("ultrafast was accepted; only the five offered presets are valid")
	}
}

func TestEncodeArgsVideoCopy(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:  "master.mkv",
		Output: "out.mkv",
		Video:  VideoPlan{Copy: true},
	})

	if !strings.Contains(got, "-c:v copy") {
		t.Errorf("expected a stream copy\ngot: %s", got)
	}
	if strings.Contains(got, "libx265") {
		t.Errorf("copy plan still invoked the encoder\ngot: %s", got)
	}
}

// The whole point of §9: mastering display and MaxCLL must reach x265, because
// ffmpeg does not forward them and their absence yields a grey picture with no
// error anywhere.
func TestEncodeArgsPropagatesHDR(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:            "master.mkv",
		Output:           "out.mkv",
		VideoSourceIndex: 0,
		Video:            VideoPlan{CRF: 20, Preset: PresetSlow},
		Color: ColorInfo{
			Primaries: "bt2020",
			Transfer:  "smpte2084",
			Space:     "bt2020nc",
		},
		HDR: &HDR{
			GreenX: 13250, GreenY: 34500,
			BlueX: 7500, BlueY: 3000,
			RedX: 34000, RedY: 16000,
			WhiteX: 15635, WhiteY: 16450,
			MaxLuminance: 10000000, MinLuminance: 50,
			MaxCLL: 1000, MaxFALL: 400,
		},
	})

	for _, want := range []string{
		"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)",
		"max-cll=1000,400",
		"hdr10=1",
		"colorprim=bt2020",
		"transfer=smpte2084",
		"-color_primaries bt2020",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HDR metadata missing %q\ngot: %s", want, got)
		}
	}
}

// An SDR source must not acquire HDR flags it never had.
func TestEncodeArgsSDRHasNoHDRParams(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:  "master.mkv",
		Output: "out.mkv",
		Video:  VideoPlan{CRF: 18, Preset: PresetSlow},
		Color:  ColorInfo{Primaries: "bt709", Transfer: "bt709", Space: "bt709"},
	})

	for _, unwanted := range []string{"hdr10=1", "master-display", "max-cll"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("SDR encode carries %q\ngot: %s", unwanted, got)
		}
	}
}

func TestEncodeArgsAudioCopyAndFallback(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:            "master.mkv",
		Output:           "out.mkv",
		VideoSourceIndex: 0,
		Video:            VideoPlan{CRF: 20, Preset: PresetSlow},
		Audio: []AudioTrack{
			{SourceIndex: 2, Copy: true, Lang: "eng", Title: "Surround 5.1", Default: true},
			{SourceIndex: 1, Codec: "aac", Bitrate: "256k", Channels: 2, Lang: "eng", Title: "Stereo"},
		},
	})

	if !strings.Contains(got, "-c:a:0 copy") {
		t.Errorf("first track should be copied\ngot: %s", got)
	}
	if !strings.Contains(got, "-c:a:1 aac") || !strings.Contains(got, "-b:a:1 256k") {
		t.Errorf("stereo fallback missing\ngot: %s", got)
	}
	if !strings.Contains(got, "-ac:a:1 2") {
		t.Errorf("stereo downmix missing\ngot: %s", got)
	}
	// Apple TV picks the first compatible track, so multichannel is mapped first.
	if strings.Index(got, "-map 0:2") > strings.Index(got, "-map 0:1") {
		t.Errorf("multichannel track is not mapped first\ngot: %s", got)
	}
}

func TestEncodeArgsSubtitles(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:            "master.mkv",
		Output:           "out.mkv",
		VideoSourceIndex: 0,
		Video:            VideoPlan{CRF: 20, Preset: PresetSlow},
		Subtitles: []SubtitleTrack{
			{SourceIndex: 6, Lang: "fra", Title: "French"},
			{SourceIndex: 5, Lang: "eng", Title: "English", Forced: true, Default: true},
		},
	})

	// In the order given, each copied as it is.
	for _, want := range []string{
		"-map 0:6 -map 0:5",
		"-c:s:0 copy", "-metadata:s:s:0 language=fra", "-disposition:s:0 0",
		"-c:s:1 copy", "-disposition:s:1 default+forced",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\ngot: %s", want, got)
		}
	}
}

func TestDispositionClearsInheritedFlags(t *testing.T) {
	// "0" rather than empty: ffmpeg otherwise carries the source's flags over.
	if got := dispositionOf(false, false); got != "0" {
		t.Errorf("dispositionOf(false,false) = %q, want \"0\"", got)
	}
}

// "-ac:N" counts every stream in the file, not the audio ones, so the bare
// form silently applied the downmix to the wrong stream and the track came out
// with all its channels intact.
func TestChannelCountUsesAnAudioStreamSpecifier(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input:            "master.mkv",
		Output:           "out.mkv",
		VideoSourceIndex: 0,
		Video:            VideoPlan{CRF: 20, Preset: PresetSlow},
		Audio: []AudioTrack{
			{SourceIndex: 1, Codec: "eac3", Bitrate: "768k"},
			{SourceIndex: 1, Codec: "aac", Bitrate: "256k", Channels: 2},
		},
	})

	if !strings.Contains(got, "-ac:a:1 2") {
		t.Errorf("the downmix does not use an audio stream specifier: %s", got)
	}
	if strings.Contains(got, "-ac:1 ") {
		t.Errorf("the bare form is still being used, which applies to the wrong stream: %s", got)
	}
}

// TrueHD made into FLAC gets even frames, since a cut can start on a frame too
// small for the encoder to begin with. Nothing else does.
func TestLosslessEncodesGetEvenFrames(t *testing.T) {
	got := argString(t, EncodeRequest{
		Input: "master.mkv", Output: "out.mkv",
		Video: VideoPlan{Copy: true},
		Audio: []AudioTrack{
			{SourceIndex: 1, Codec: "aac", Bitrate: "256k"},
			{SourceIndex: 2, Codec: "flac"},
		},
	})
	if !strings.Contains(got, "-c:a:1 flac -filter:a:1 asetnsamples=n=4096:p=0") {
		t.Errorf("the FLAC track is not given even frames\ngot: %s", got)
	}
	if strings.Contains(got, "-filter:a:0") {
		t.Errorf("the AAC track was filtered for no reason\ngot: %s", got)
	}
}

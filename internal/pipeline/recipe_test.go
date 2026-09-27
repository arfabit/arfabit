package pipeline

import (
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/store"
)

func masterOf(tracks ...Track) []Track { return tracks }

func describe(items []store.Item) string {
	var parts []string
	for _, it := range items {
		part := it.Kind + ":" + it.Lang + ":" + it.Codec + ":" + it.Action
		if it.To != "" {
			part += ">" + it.To
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// The example this design was agreed on: a blueprint keeps one of English,
// French or Japanese, and the original has only Japanese. After the blueprint
// is used, Japanese is what is in the package — exactly what adding it by
// hand would have made.
func TestRecipeKeepsTheOneLanguageThereIs(t *testing.T) {
	tracks := masterOf(
		Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080},
		Track{Index: 1, Kind: store.KindAudio, Codec: "truehd", Lang: "jpn", Channels: 6, Lossless: true},
		Track{Index: 2, Kind: store.KindAudio, Codec: "ac3", Lang: "jpn", Channels: 2},
	)
	b := config.Defaults().Plain()
	b.Sound = &config.SoundRules{
		Languages: []string{"eng", "fra", "jpn"}, LanguageMode: config.SoundOne,
		Choices: []config.SoundChoice{{Mode: config.SoundOne, Layouts: []string{"7.1", "5.1"}}},
	}

	pkg := Recipe(tracks, b, false)
	if got := describe(pkg.ItemsOf(store.KindAudio)); got != "audio:jpn:truehd:copy" {
		t.Errorf("sound = %q, want the Japanese 5.1 kept as it is", got)
	}
}

// A blueprint whose rules fit nothing adds no sound: the user adds what they
// want, rather than ARFABIT guessing.
func TestRecipeAddsNoSoundWhenNothingFits(t *testing.T) {
	tracks := masterOf(
		Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080},
		Track{Index: 1, Kind: store.KindAudio, Codec: "ac3", Lang: "deu", Channels: 6},
	)
	b := config.Defaults().Plain()
	b.Sound = &config.SoundRules{Languages: []string{"eng"}, Choices: []config.SoundChoice{{Mode: config.SoundAll}}}

	pkg := Recipe(tracks, b, false)
	if n := len(pkg.ItemsOf(store.KindAudio)); n != 0 {
		t.Errorf("%d sound line items were added from rules that fit nothing", n)
	}
	if n := len(pkg.ItemsOf(store.KindVideo)); n != 1 {
		t.Errorf("the picture line is missing")
	}
}

// TrueHD is kept as it is unless the blueprint says otherwise; "both" keeps it
// and adds FLAC as a second line below it.
func TestRecipeTrueHDChoice(t *testing.T) {
	tracks := masterOf(
		Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080},
		Track{Index: 1, Kind: store.KindAudio, Codec: "truehd", Lang: "eng", Channels: 8, Lossless: true},
	)
	everything := &config.SoundRules{Choices: []config.SoundChoice{{Mode: config.SoundAll, Layouts: []string{"7.1"}}}}

	for choice, want := range map[string]string{
		"":                "audio:eng:truehd:copy",
		config.TrueHDKeep: "audio:eng:truehd:copy",
		config.TrueHDFLAC: "audio:eng:truehd:convert>flac",
		config.TrueHDBoth: "audio:eng:truehd:copy audio:eng:truehd:convert>flac",
	} {
		b := config.Defaults().Plain()
		b.Sound, b.TrueHD = everything, choice
		if got := describe(Recipe(tracks, b, false).ItemsOf(store.KindAudio)); got != want {
			t.Errorf("TrueHD %q: sound = %q, want %q", choice, got, want)
		}
	}
}

// The picture is made into HEVC at the quality for its disc type, unless the
// blueprint keeps it as it is.
func TestRecipePicture(t *testing.T) {
	tracks := masterOf(Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080})

	b := config.Defaults().Plain()
	b.CRFBluray, b.Preset = 22, "medium"
	v := Recipe(tracks, b, false).ItemsOf(store.KindVideo)[0]
	if v.Action != store.ActionConvert || v.To != "hevc" || v.CRF != 22 || v.Preset != "medium" {
		t.Errorf("picture = %+v, want HEVC at quality 22, medium", v)
	}

	b.Video = config.VideoCopy
	if v := Recipe(tracks, b, false).ItemsOf(store.KindVideo)[0]; v.Action != store.ActionCopy {
		t.Errorf("picture = %+v, want it kept as it is", v)
	}
}

// Subtitles in the wanted languages are copied where they cannot be read
// into text; others are left out.
func TestRecipeSubtitles(t *testing.T) {
	tracks := masterOf(
		Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080},
		Track{Index: 5, Kind: store.KindSubtitle, Codec: "hdmv_pgs_subtitle", Lang: "eng"},
		Track{Index: 6, Kind: store.KindSubtitle, Codec: "hdmv_pgs_subtitle", Lang: "fra"},
	)
	b := config.Defaults().Plain() // English subtitles
	if got := describe(Recipe(tracks, b, false).ItemsOf(store.KindSubtitle)); got != "subtitle:eng:hdmv_pgs_subtitle:copy" {
		t.Errorf("subtitles = %q", got)
	}
}

// A disc's tracks are named as an original's are, so the same notes apply; the
// forced captions MakeMKV lists as a track of their own are left out, since
// the original holds them inside the one track.
func TestDiscTracks(t *testing.T) {
	tracks := DiscTracks(disc.Title{Streams: []disc.Stream{
		{Index: 0, Kind: disc.StreamVideo, CodecID: "V_MPEG4/ISO/AVC", Height: 1080},
		{Index: 1, Kind: disc.StreamAudio, CodecID: "A_TRUEHD", Lang: "eng", Channels: 8},
		{Index: 2, Kind: disc.StreamSubtitle, CodecID: "S_HDMV/PGS", Lang: "eng"},
		{Index: 3, Kind: disc.StreamSubtitle, CodecID: "S_HDMV/PGS", Lang: "eng", Forced: true},
	}})

	var got []string
	for _, tr := range tracks {
		got = append(got, tr.Kind+":"+tr.Codec)
	}
	if strings.Join(got, " ") != "video:h264 audio:truehd subtitle:hdmv_pgs_subtitle" {
		t.Errorf("tracks = %v", got)
	}
	if !tracks[1].Lossless || !strings.Contains(tracks[1].Note, "FLAC 7.1") {
		t.Errorf("the TrueHD track = %+v", tracks[1])
	}
}

// Lossy sound is never converted to more than it had: that is only a bigger
// file of the same sound. Lossless sound has no such limit.
func TestBitrateNeverExceedsTheSource(t *testing.T) {
	for _, tc := range []struct {
		want  string
		track Track
		got   string
	}{
		{"256k", Track{Bitrate: 192000}, "192k"},
		{"768k", Track{Bitrate: 640000}, "640k"},
		{"256k", Track{Bitrate: 320000}, "256k"},
		{"256k", Track{Bitrate: 96000}, "128k"},
		{"640k", Track{Bitrate: 128000, Lossless: true}, "640k"},
		{"256k", Track{}, "256k"},
	} {
		if got := bitrateFor(tc.want, tc.track); got != tc.got {
			t.Errorf("bitrateFor(%s, %d kbps) = %s, want %s", tc.want, tc.track.Bitrate/1000, got, tc.got)
		}
	}
}

// Where subtitles can be read into text, a Blu-ray's picture subtitles are
// converted to SRT, unless the blueprint keeps them as pictures. Subtitles
// that cannot be read are copied.
func TestRecipeReadsSubtitlesIntoText(t *testing.T) {
	tracks := masterOf(
		Track{Index: 0, Kind: store.KindVideo, Codec: "h264", Height: 1080},
		Track{Index: 5, Kind: store.KindSubtitle, Codec: "hdmv_pgs_subtitle", Lang: "eng"},
		Track{Index: 6, Kind: store.KindSubtitle, Codec: "dvd_subtitle", Lang: "eng"},
		Track{Index: 7, Kind: store.KindSubtitle, Codec: "hdmv_pgs_subtitle", Lang: "eng"},
	)
	b := config.Defaults().Plain()
	// The second English picture track is copied: each track read becomes a
	// file named by its language.
	if got := describe(Recipe(tracks, b, true).ItemsOf(store.KindSubtitle)); got != "subtitle:eng:hdmv_pgs_subtitle:convert>srt subtitle:eng:dvd_subtitle:copy subtitle:eng:hdmv_pgs_subtitle:copy" {
		t.Errorf("subtitles = %q", got)
	}

	b.Subtitles = config.SubtitlesPictures
	if got := describe(Recipe(tracks, b, true).ItemsOf(store.KindSubtitle)); got != "subtitle:eng:hdmv_pgs_subtitle:copy subtitle:eng:dvd_subtitle:copy subtitle:eng:hdmv_pgs_subtitle:copy" {
		t.Errorf("kept as pictures: subtitles = %q", got)
	}
}

package makemkv

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
)

func TestSplitFields(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", `0,2,999,12`, []string{"0", "2", "999", "12"}},
		{"quoted", `1,0,"hello"`, []string{"1", "0", "hello"}},
		{
			"escaped quotes",
			`2010,0,1,"Optical drive \"BD-RE\" opened."`,
			[]string{"2010", "0", "1", `Optical drive "BD-RE" opened.`},
		},
		{
			// A comma inside a quoted title must not split the field. This is
			// the case that breaks naive strings.Split parsers.
			"comma inside string",
			`0,30,0,"The Sheep Detectives - 16 chapter(s) , 31.1 GB"`,
			[]string{"0", "30", "0", "The Sheep Detectives - 16 chapter(s) , 31.1 GB"},
		},
		{"empty strings", `1,256,999,0,"","",""`, []string{"1", "256", "999", "0", "", "", ""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitFields(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d fields %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("field %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseLineRejectsNonRecords(t *testing.T) {
	for _, in := range []string{"", "no colon here", "lowercase:1,2", "  "} {
		if _, ok := ParseLine(in); ok {
			t.Errorf("ParseLine(%q) accepted a non-record", in)
		}
	}
}

func loadFixture(t *testing.T, name string) *ScanResult {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	res, err := ParseScan(f)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestParseScanDisc(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")

	if got, want := res.Disc.Kind, disc.KindBluray; got != want {
		t.Errorf("Kind = %q, want %q", got, want)
	}
	// Attr 2 is preferred over the volume label: it is already human readable.
	if got, want := res.Disc.Name, "The Sheep Detectives"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
	if got, want := res.Disc.Label, "THE_SHEEP_DETECTIVES"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
}

func TestParseScanDrivesSkipEmptySlots(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")

	// MakeMKV prints sixteen DRV lines; fifteen are empty slots.
	if got, want := len(res.Drives), 1; got != want {
		t.Fatalf("got %d drives, want %d", got, want)
	}
	d := res.Drives[0]
	if d.Device != "/dev/rdisk8" {
		t.Errorf("Device = %q, want /dev/rdisk8", d.Device)
	}
	if !d.Loaded {
		t.Error("Loaded = false, want true")
	}
}

func TestParseScanTitle(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")

	if got, want := len(res.Disc.Titles), 1; got != want {
		t.Fatalf("got %d titles, want %d", got, want)
	}

	title := res.Disc.Titles[0]
	if got, want := title.Duration, 1*time.Hour+49*time.Minute+4*time.Second; got != want {
		t.Errorf("Duration = %v, want %v", got, want)
	}
	if got, want := title.SizeBytes, int64(33457569792); got != want {
		t.Errorf("SizeBytes = %d, want %d", got, want)
	}
	if got, want := title.Chapters, 16; got != want {
		t.Errorf("Chapters = %d, want %d", got, want)
	}
	if got, want := title.SourceFile, "00001.mpls"; got != want {
		t.Errorf("SourceFile = %q, want %q", got, want)
	}
	if got, want := len(title.Streams), 32; got != want {
		t.Errorf("got %d streams, want %d", got, want)
	}
}

func TestParseScanVideoStream(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")
	v := res.Disc.Titles[0].Streams[0]

	if v.Kind != disc.StreamVideo {
		t.Fatalf("Kind = %q, want video", v.Kind)
	}
	if v.Width != 1920 || v.Height != 1080 {
		t.Errorf("resolution = %dx%d, want 1920x1080", v.Width, v.Height)
	}
	if v.CodecID != "V_MPEG4/ISO/AVC" {
		t.Errorf("CodecID = %q", v.CodecID)
	}
	if v.AspectRatio != "16:9" {
		t.Errorf("AspectRatio = %q, want 16:9", v.AspectRatio)
	}
}

func TestParseScanAudioStream(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")
	a := res.Disc.Titles[0].Streams[1]

	if a.Kind != disc.StreamAudio {
		t.Fatalf("Kind = %q, want audio", a.Kind)
	}
	if a.CodecID != "A_TRUEHD" {
		t.Errorf("CodecID = %q, want A_TRUEHD", a.CodecID)
	}
	if a.Channels != 8 || a.Layout != "7.1" {
		t.Errorf("channels = %d layout = %q, want 8 / 7.1", a.Channels, a.Layout)
	}
	if !a.Default {
		t.Error("Default = false; this track carries the 'd' flag")
	}
}

// Attributes 28/29 hold the *title's* language, not the stream's. A parser that
// reads them labels every track English. Stream 14 is French and is the
// regression guard.
func TestSubtitleLanguageUsesStreamAttribute(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")
	s := res.Disc.Titles[0].Streams[14]

	if s.Kind != disc.StreamSubtitle {
		t.Fatalf("Kind = %q, want subtitle", s.Kind)
	}
	if got, want := s.Lang, "fra"; got != want {
		t.Errorf("Lang = %q, want %q — attr 28/29 were probably read instead of 3/4", got, want)
	}
}

func TestForcedSubtitleInference(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")
	streams := res.Disc.Titles[0].Streams

	// Stream 10 is a full English track, 11 its forced-only counterpart.
	if streams[10].Forced {
		t.Error("stream 10 inferred as forced, want not forced")
	}
	if !streams[11].Forced {
		t.Error("stream 11 not inferred as forced")
	}
	// The disc marks the forced English track as default, which is what §10
	// relies on to enable forced subtitles automatically.
	if !streams[11].Default {
		t.Error("stream 11 Default = false, want true")
	}
}

func TestParseScanMinLengthZero(t *testing.T) {
	res := loadFixture(t, "scan-bd-1-minlen0.txt")

	// With the length filter off, MakeMKV also lists menus and stingers.
	if got, want := len(res.Disc.Titles), 16; got != want {
		t.Fatalf("got %d titles, want %d", got, want)
	}

	// The feature dwarfs everything else, so main-feature selection is
	// unambiguous on this disc.
	feature := res.Disc.Titles[0]
	for _, other := range res.Disc.Titles[1:] {
		if other.SizeBytes >= feature.SizeBytes {
			t.Errorf("title %d (%d bytes) is not smaller than the feature (%d bytes)",
				other.Index, other.SizeBytes, feature.SizeBytes)
		}
	}
}

func TestNoisyMessagesAreDropped(t *testing.T) {
	res := loadFixture(t, "scan-bd-1.txt")
	for _, m := range res.Messages {
		if m.Code == msgProfileMissing || m.Code == msgVersion {
			t.Errorf("message %d should have been suppressed: %q", m.Code, m.Text)
		}
	}
}

// A scan must survive record types and attributes it does not know, because
// MakeMKV adds them between versions.
func TestParseScanIgnoresUnknownRecords(t *testing.T) {
	in := strings.Join([]string{
		`CINFO:1,6209,"Blu-ray disc"`,
		`FUTURE:1,2,3,"something new"`,
		`TINFO:0,9,0,"1:30:00"`,
		`TINFO:0,9999,0,"unknown attribute"`,
	}, "\n")

	res, err := ParseScan(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Disc.Titles) != 1 {
		t.Fatalf("got %d titles, want 1", len(res.Disc.Titles))
	}
	if got, want := res.Disc.Titles[0].Duration, 90*time.Minute; got != want {
		t.Errorf("Duration = %v, want %v", got, want)
	}
}

// A second real disc, with three English subtitle sets and five French ones —
// the duplicate-track case Appendix A describes. It guards against a parser
// that collapses same-language streams or miscounts them.
func TestParseScanSecondDisc(t *testing.T) {
	res := loadFixture(t, "scan-bd-2.txt")

	if got, want := res.Disc.Name, "Crime 101"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}

	title := res.Disc.Titles[0]
	if got, want := len(title.Streams), 49; got != want {
		t.Fatalf("got %d streams, want %d", got, want)
	}

	langs := map[string]int{}
	var audio, subs int
	for _, s := range title.Streams {
		switch s.Kind {
		case disc.StreamAudio:
			audio++
		case disc.StreamSubtitle:
			subs++
			langs[s.Lang]++
		}
	}
	if audio != 12 || subs != 36 {
		t.Errorf("audio = %d subs = %d, want 12 / 36", audio, subs)
	}
	// Seven subtitle languages, none of which should be mislabelled English.
	if got, want := len(langs), 7; got != want {
		t.Errorf("got %d subtitle languages %v, want %d", got, langs, want)
	}
	if got, want := langs["jpn"], 4; got != want {
		t.Errorf("Japanese subtitle streams = %d, want %d", got, want)
	}
}

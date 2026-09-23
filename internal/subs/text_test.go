package subs

import (
	"strings"
	"testing"
	"time"
)

func TestSRTTime(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00:00,000"},
		{90 * time.Second, "00:01:30,000"},
		{time.Hour + 2*time.Minute + 3*time.Second + 456*time.Millisecond, "01:02:03,456"},
		{-time.Second, "00:00:00,000"},
	}
	for _, tc := range tests {
		if got := srtTime(tc.in); got != tc.want {
			t.Errorf("srtTime(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteSRT(t *testing.T) {
	got := WriteSRT([]Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "Hello there."},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "General\nKenobi."},
	})

	want := "1\n00:00:01,000 --> 00:00:03,000\nHello there.\n\n" +
		"2\n00:00:04,000 --> 00:00:06,000\nGeneral\nKenobi.\n\n"

	if got != want {
		t.Errorf("SRT output:\n%q\nwant:\n%q", got, want)
	}
}

// An unrecognised shape is marked rather than dropped. A sentence that reads
// almost right is worse than one that plainly says something is missing.
func TestUnknownShapesAreVisible(t *testing.T) {
	img := draw(
		"## ##",
		"#  ##",
	)
	glyphs := SplitGlyphs(img)[0].Glyphs

	// Only the first shape is known.
	alphabet := Alphabet{glyphs[0].Key: "A"}

	cues := Recognise([]Subtitle{{Start: 0, End: time.Second, Image: img}}, alphabet)
	if len(cues) != 1 {
		t.Fatalf("got %d cues, want 1", len(cues))
	}

	if !strings.Contains(cues[0].Text, Unknown) {
		t.Errorf("an unrecognised shape was dropped rather than marked: %q", cues[0].Text)
	}
	if cues[0].Unknowns != 1 {
		t.Errorf("Unknowns = %d, want 1", cues[0].Unknowns)
	}
}

// Spaces are measured from the line rather than assumed, because subtitles are
// set at whatever size the disc chose.
func TestWordSpacesAreFoundFromTheLine(t *testing.T) {
	// Four marks: two close together, a wide gap, then two more.
	img := draw(
		"# #     # #",
		"# #     # #",
	)

	line := SplitGlyphs(img)[0]
	if len(line.Glyphs) != 4 {
		t.Fatalf("got %d glyphs, want 4", len(line.Glyphs))
	}

	alphabet := Alphabet{}
	for i, g := range line.Glyphs {
		alphabet[g.Key] = string(rune('a' + i))
	}

	text, _ := readLine(line, alphabet)
	if !strings.Contains(text, " ") {
		t.Errorf("no space was found in %q despite a wide gap", text)
	}
	if strings.Count(text, " ") != 1 {
		t.Errorf("got %d spaces in %q, want 1", strings.Count(text, " "), text)
	}
}

// Every mark the same distance apart is one word, however many marks there are.
func TestEvenlySpacedMarksAreOneWord(t *testing.T) {
	img := draw(
		"# # # # #",
		"# # # # #",
	)

	line := SplitGlyphs(img)[0]
	alphabet := Alphabet{}
	for _, g := range line.Glyphs {
		alphabet[g.Key] = "x"
	}

	if text, _ := readLine(line, alphabet); strings.Contains(text, " ") {
		t.Errorf("evenly spaced marks were split into words: %q", text)
	}
}

func TestCoverageDescribes(t *testing.T) {
	perfect := Coverage{Cues: 1200, Glyphs: 60000}
	if !strings.Contains(perfect.Describe(), "All 1200") {
		t.Errorf("a perfect read does not say so: %q", perfect.Describe())
	}

	partial := Coverage{Cues: 1200, Glyphs: 60000, Unknowns: 60}
	got := partial.Describe()
	if !strings.Contains(got, "60 marks") || !strings.Contains(got, "99.9") {
		t.Errorf("a partial read does not say how partial: %q", got)
	}

	if (Coverage{}).Percent() != 0 {
		t.Error("an empty coverage should be zero, not a division by zero")
	}
}

// An empty subtitle produces no cue rather than a blank one.
func TestBlankSubtitlesAreDropped(t *testing.T) {
	cues := Recognise([]Subtitle{{Start: 0, End: time.Second, Image: draw("   ", "   ")}}, Alphabet{})
	if len(cues) != 0 {
		t.Errorf("got %d cues from a blank subtitle", len(cues))
	}
}

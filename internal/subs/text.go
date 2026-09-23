package subs

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Alphabet maps a glyph shape to the character it stands for.
//
// It is learned once per font and then reused: a disc uses one font
// throughout, and studios reuse fonts across releases, so the second disc from
// a studio is usually free.
type Alphabet map[string]string

// Unknown is what stands in for a shape nobody has identified yet.
//
// A visible marker is deliberate. A silently dropped character produces a
// sentence that reads almost right, which is far worse than one that plainly
// says something is missing.
const Unknown = "�"

// Cue is one subtitle as text.
type Cue struct {
	Start time.Duration
	End   time.Duration
	Text  string

	// Unknowns is how many shapes in this cue were not recognised, so the
	// page can show what needs looking at rather than making people hunt.
	Unknowns int
}

// Recognise turns subtitle images into text using a known alphabet.
func Recognise(subtitles []Subtitle, alphabet Alphabet) []Cue {
	cues := make([]Cue, 0, len(subtitles))

	for _, s := range subtitles {
		text, unknowns := readImage(s, alphabet)
		if strings.TrimSpace(text) == "" {
			continue
		}
		cues = append(cues, Cue{
			Start:    s.Start,
			End:      s.End,
			Text:     text,
			Unknowns: unknowns,
		})
	}

	return cues
}

// readImage reads one subtitle.
func readImage(s Subtitle, alphabet Alphabet) (string, int) {
	var (
		lines    []string
		unknowns int
	)

	for _, line := range SplitGlyphs(s.Image) {
		text, missing := readLine(line, alphabet)
		unknowns += missing
		if text != "" {
			lines = append(lines, text)
		}
	}

	return strings.Join(lines, "\n"), unknowns
}

// readLine reads one row of glyphs, putting the spaces back.
func readLine(line Line, alphabet Alphabet) (string, int) {
	if len(line.Glyphs) == 0 {
		return "", 0
	}

	gap := wordGap(line)

	var (
		b        strings.Builder
		unknowns int
	)

	for i, g := range line.Glyphs {
		if i > 0 {
			if g.Bounds.Min.X-line.Glyphs[i-1].Bounds.Max.X >= gap {
				b.WriteByte(' ')
			}
		}

		if ch, ok := alphabet[g.Key]; ok {
			b.WriteString(ch)
			continue
		}
		b.WriteString(Unknown)
		unknowns++
	}

	return strings.TrimSpace(b.String()), unknowns
}

// wordGap works out how wide a gap has to be to mean a space.
//
// It is measured from the line itself rather than assumed: subtitles are set
// at whatever size the disc chose, so a fixed number of pixels would put
// spaces in the middle of words on one disc and lose them on another.
func wordGap(line Line) int {
	if len(line.Glyphs) < 3 {
		// Too little to measure from; a gap the height of the text is a
		// reasonable stand-in.
		return line.Bounds.Dy() / 3
	}

	gaps := make([]int, 0, len(line.Glyphs)-1)
	for i := 1; i < len(line.Glyphs); i++ {
		gaps = append(gaps, line.Glyphs[i].Bounds.Min.X-line.Glyphs[i-1].Bounds.Max.X)
	}
	sort.Ints(gaps)

	// Most gaps are between letters, so the middle one is a letter gap. A word
	// gap is comfortably wider than that.
	median := gaps[len(gaps)/2]
	gap := median*2 + 2

	if minimum := line.Bounds.Dy() / 4; gap < minimum {
		gap = minimum
	}
	return gap
}

// WriteSRT renders cues as a subtitle file.
func WriteSRT(cues []Cue) string {
	var b strings.Builder

	for i, cue := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n",
			i+1,
			srtTime(cue.Start),
			srtTime(cue.End),
			cue.Text,
		)
	}

	return b.String()
}

// srtTime renders a timestamp in the form SRT requires, down to the
// millisecond and with a comma before it.
func srtTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	hours := int(d / time.Hour)
	minutes := int(d/time.Minute) % 60
	seconds := int(d/time.Second) % 60
	millis := int(d/time.Millisecond) % 1000

	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, millis)
}

// Coverage reports how much of a film was read successfully.
//
// Shown to the user rather than kept quiet: a subtitle file that is 98% read
// is useful and worth having, and one that is 40% read is not, and only the
// person watching can decide.
type Coverage struct {
	Cues     int
	Glyphs   int
	Unknowns int
}

// Percent is the share of shapes that were recognised.
func (c Coverage) Percent() float64 {
	if c.Glyphs == 0 {
		return 0
	}
	return float64(c.Glyphs-c.Unknowns) / float64(c.Glyphs) * 100
}

// Describe says how it went, in plain words.
func (c Coverage) Describe() string {
	switch {
	case c.Glyphs == 0:
		return "No subtitles were read."
	case c.Unknowns == 0:
		return fmt.Sprintf("All %d subtitles were read.", c.Cues)
	default:
		return fmt.Sprintf("%d subtitles were read, with %d marks ARFABIT did not recognise (%.1f%% understood).",
			c.Cues, c.Unknowns, c.Percent())
	}
}

// Measure counts what was recognised.
func Measure(cues []Cue, subtitles []Subtitle) Coverage {
	coverage := Coverage{Cues: len(cues)}

	for _, cue := range cues {
		coverage.Unknowns += cue.Unknowns
	}
	for _, s := range subtitles {
		for _, line := range SplitGlyphs(s.Image) {
			coverage.Glyphs += len(line.Glyphs)
		}
	}

	return coverage
}

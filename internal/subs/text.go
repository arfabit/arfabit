package subs

import (
	"fmt"
	"strings"
	"time"
)

// Cue is one subtitle as text.
type Cue struct {
	Start time.Duration
	End   time.Duration
	Text  string
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

// ParseSRT reads a subtitle file, such as one WriteSRT wrote and a person may
// since have changed. A block it cannot make sense of is an error rather than
// something quietly dropped: the file is about to be written back.
func ParseSRT(data string) ([]Cue, error) {
	data = strings.ReplaceAll(strings.TrimPrefix(data, "\uFEFF"), "\r\n", "\n")
	var cues []Cue
	for _, block := range strings.Split(strings.TrimSpace(data), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		if len(lines) < 2 {
			return nil, fmt.Errorf("a subtitle has no times: %q", block)
		}
		from, to, ok := strings.Cut(lines[1], " --> ")
		if !ok {
			return nil, fmt.Errorf("a subtitle's times cannot be read: %q", lines[1])
		}
		start, err := parseSRTTime(from)
		if err != nil {
			return nil, err
		}
		end, err := parseSRTTime(to)
		if err != nil {
			return nil, err
		}
		cues = append(cues, Cue{Start: start, End: end, Text: strings.Join(lines[2:], "\n")})
	}
	return cues, nil
}

func parseSRTTime(s string) (time.Duration, error) {
	var h, m, sec, ms int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d:%d,%d", &h, &m, &sec, &ms); err != nil {
		return 0, fmt.Errorf("%q is not a subtitle time", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond, nil
}

// SetCue gives the subtitle shown from start the text given: changing it, or
// putting it in where it was left out, or taking it out when the text is
// empty. Every other subtitle is kept as it is.
func SetCue(cues []Cue, start, end time.Duration, text string) []Cue {
	start = start.Truncate(time.Millisecond)
	out := make([]Cue, 0, len(cues)+1)
	placed := false
	for _, c := range cues {
		if c.Start == start {
			placed = true
			if text != "" {
				c.Text = text
				out = append(out, c)
			}
			continue
		}
		if !placed && c.Start > start && text != "" {
			out = append(out, Cue{Start: start, End: end, Text: text})
			placed = true
		}
		out = append(out, c)
	}
	if !placed && text != "" {
		out = append(out, Cue{Start: start, End: end, Text: text})
	}
	return out
}

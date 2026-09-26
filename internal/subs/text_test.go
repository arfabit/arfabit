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

// A file WriteSRT wrote reads back as it was, and one subtitle can be changed,
// put in or taken out while every other stays as it is.
func TestParseAndSetCue(t *testing.T) {
	s := time.Second
	cues := []Cue{
		{Start: 1 * s, End: 2 * s, Text: "So do I."},
		{Start: 5*s + 250*time.Millisecond, End: 6 * s, Text: "'Course | am.\nSecond line"},
	}
	got, err := ParseSRT("\uFEFF" + strings.ReplaceAll(WriteSRT(cues), "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != cues[0] || got[1] != cues[1] {
		t.Fatalf("read back %+v", got)
	}

	changed := SetCue(got, 5*s+250*time.Millisecond, 6*s, "'Course I am.\nSecond line")
	if changed[1].Text != "'Course I am.\nSecond line" || changed[0] != cues[0] {
		t.Errorf("changed = %+v", changed)
	}
	added := SetCue(got, 3*s, 4*s, "I...")
	if len(added) != 3 || added[1].Text != "I..." || added[1].End != 4*s {
		t.Errorf("added = %+v", added)
	}
	removed := SetCue(got, 1*s, 2*s, "")
	if len(removed) != 1 || removed[0] != cues[1] {
		t.Errorf("removed = %+v", removed)
	}

	if _, err := ParseSRT("1\nnot a time\nHello\n"); err == nil {
		t.Error("a subtitle with unreadable times was accepted")
	}
}

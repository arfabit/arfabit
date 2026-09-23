package ffmpeg

import (
	"strings"
	"testing"
	"time"
)

// A real -progress block, which ffmpeg emits repeatedly during an encode.
const progressBlock = `frame=1440
fps=24.5
stream_0_0_q=28.0
bitrate=5000.0kbits/s
total_size=37500000
out_time_us=60000000
out_time=00:01:00.000000
speed=0.98x
progress=continue
`

func TestReadProgress(t *testing.T) {
	var got []Progress
	readProgress(strings.NewReader(progressBlock), RunOptions{
		Duration:   90 * time.Minute,
		OnProgress: func(p Progress) { got = append(got, p) },
	})

	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	p := got[0]

	if p.Position != time.Minute {
		t.Errorf("Position = %v, want 1m", p.Position)
	}
	if p.FPS != 24.5 {
		t.Errorf("FPS = %v, want 24.5", p.FPS)
	}
	if p.Speed != 0.98 {
		t.Errorf("Speed = %v, want 0.98 (the trailing x must be stripped)", p.Speed)
	}
	if p.Bytes != 37500000 {
		t.Errorf("Bytes = %d", p.Bytes)
	}
}

func TestProgressPercent(t *testing.T) {
	p := Progress{Position: 45 * time.Minute, Duration: 90 * time.Minute}
	if got := p.Percent(); got != 50 {
		t.Errorf("Percent = %v, want 50", got)
	}

	// An unknown duration must report zero rather than a misleading number.
	if got := (Progress{Position: time.Minute}).Percent(); got != 0 {
		t.Errorf("Percent with unknown duration = %v, want 0", got)
	}

	// ffmpeg occasionally reports a position past the end.
	over := Progress{Position: 100 * time.Minute, Duration: 90 * time.Minute}
	if got := over.Percent(); got != 100 {
		t.Errorf("Percent = %v, want it clamped to 100", got)
	}
}

func TestProgressRemaining(t *testing.T) {
	// Half of a 90-minute film left, encoding at half real time.
	p := Progress{Position: 45 * time.Minute, Duration: 90 * time.Minute, Speed: 0.5}
	if got, want := p.Remaining(), 90*time.Minute; got != want {
		t.Errorf("Remaining = %v, want %v", got, want)
	}

	// No speed yet, so no estimate rather than a wrong one.
	if got := (Progress{Position: time.Minute, Duration: time.Hour}).Remaining(); got != 0 {
		t.Errorf("Remaining without a speed = %v, want 0", got)
	}
}

// ffmpeg's stderr can run to megabytes; the end is the part that explains a
// failure, so that is what is kept.
func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{limit: 100}
	for i := 0; i < 1000; i++ {
		if _, err := tb.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}

	got := tb.String()
	if len(got) > 110 {
		t.Errorf("buffer grew to %d bytes, want it bounded near 100", len(got))
	}
	if !strings.HasSuffix(got, "0123456789") {
		t.Errorf("tail does not end with the last written data: %q", got)
	}
}

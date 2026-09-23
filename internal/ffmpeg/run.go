package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Progress is one update during an encode.
type Progress struct {
	// Position is how far into the media the encoder has reached.
	Position time.Duration

	// Duration is the total length, from the probe. Zero when unknown.
	Duration time.Duration

	// Speed is the encode rate relative to real time, e.g. 0.8 means the
	// encode takes longer than the film runs.
	Speed float64

	// FPS is frames encoded per second.
	FPS float64

	// Bytes written so far.
	Bytes int64
}

// Percent is progress through the media, 0-100. Zero when the duration is
// unknown rather than a misleading number.
func (p Progress) Percent() float64 {
	if p.Duration <= 0 {
		return 0
	}
	pct := float64(p.Position) / float64(p.Duration) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

// Remaining estimates the time left, or zero when it cannot be known.
func (p Progress) Remaining() time.Duration {
	if p.Duration <= 0 || p.Speed <= 0 || p.Position >= p.Duration {
		return 0
	}
	return time.Duration(float64(p.Duration-p.Position) / p.Speed)
}

// RunOptions control one ffmpeg invocation.
type RunOptions struct {
	// Duration of the source, used to turn positions into percentages.
	Duration time.Duration

	// OnProgress is called as the encode advances. Optional; must not block.
	OnProgress func(Progress)
}

// Run executes ffmpeg with the given arguments.
//
// Progress is read from -progress on stdout, which emits structured key=value
// lines. ffmpeg's ordinary stderr output is human-oriented and awkward to
// parse; -progress exists precisely so that it need not be.
func Run(ctx context.Context, args []string, opts RunOptions) error {
	bin, err := Locate()
	if err != nil {
		return err
	}

	// -progress writes to stdout, -nostats suppresses the duplicate
	// human-readable stream on stderr.
	full := append([]string{"-nostats", "-progress", "pipe:1"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	// ffmpeg reports real errors on stderr. Keep the tail: the last lines say
	// what went wrong, and the whole stream can be enormous.
	stderr := &tailBuffer{limit: 64 * 1024}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start ffmpeg: %w", err)
	}

	readProgress(stdout, opts)
	_, _ = io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		return &RunError{Err: err, Output: stderr.String()}
	}
	return nil
}

// RunError carries ffmpeg's own output alongside the failure.
//
// Per §15 the raw output is always preserved: ARFABIT never replaces it with a
// guess about what went wrong.
type RunError struct {
	Err    error
	Output string
}

func (e *RunError) Error() string {
	return fmt.Sprintf("ffmpeg did not finish: %v", e.Err)
}

func (e *RunError) Unwrap() error { return e.Err }

// readProgress consumes ffmpeg's -progress stream.
//
// The format is key=value lines, one field per line, terminated by a
// "progress=continue" or "progress=end" line marking the end of each block.
func readProgress(r io.Reader, opts RunOptions) {
	if opts.OnProgress == nil {
		return
	}

	sc := bufio.NewScanner(r)
	cur := Progress{Duration: opts.Duration}

	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		switch key {
		case "out_time_us", "out_time_ms":
			// Both are microseconds despite the name of the second.
			if us, err := strconv.ParseInt(value, 10, 64); err == nil {
				cur.Position = time.Duration(us) * time.Microsecond
			}
		case "total_size":
			cur.Bytes, _ = strconv.ParseInt(value, 10, 64)
		case "fps":
			cur.FPS, _ = strconv.ParseFloat(value, 64)
		case "speed":
			cur.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(value, "x"), 64)
		case "progress":
			opts.OnProgress(cur)
		}
	}
}

// tailBuffer keeps the last limit bytes written to it.
//
// ffmpeg's stderr can run to megabytes on a long encode, and it is the end that
// says what went wrong.
type tailBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n, err := t.buf.Write(p)
	if t.buf.Len() > t.limit*2 {
		keep := t.buf.Bytes()[t.buf.Len()-t.limit:]
		trimmed := make([]byte, len(keep))
		copy(trimmed, keep)
		t.buf.Reset()
		t.buf.Write(trimmed)
	}
	return n, err
}

func (t *tailBuffer) String() string {
	s := t.buf.String()
	if len(s) > t.limit {
		return "..." + s[len(s)-t.limit:]
	}
	return s
}

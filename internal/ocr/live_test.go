package ocr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/subs"
)

// Only the operating system's own reader says how well subtitles read, so
// this runs it on a real master's track when one is named:
//
//	ARFABIT_OCR_MASTER=/path/to/master.mkv ARFABIT_OCR_STREAM=3 \
//	  go test ./internal/ocr -run Live -v
//
// ARFABIT_OCR_LANG sets the language (default "eng"). Every line read is
// written to ARFABIT_OCR_OUT if set, as an SRT, to compare by eye, and every
// picture as it was given to the reader to the folder ARFABIT_OCR_PICTURES,
// if set, named by when it shows (01-18-37.129.png).
func TestLiveRead(t *testing.T) {
	master := os.Getenv("ARFABIT_OCR_MASTER")
	if master == "" {
		t.Skip("set ARFABIT_OCR_MASTER and ARFABIT_OCR_STREAM to read a real track")
	}
	stream, err := strconv.Atoi(os.Getenv("ARFABIT_OCR_STREAM"))
	if err != nil {
		t.Fatal("ARFABIT_OCR_STREAM must be the subtitle track's stream number")
	}
	lang := os.Getenv("ARFABIT_OCR_LANG")
	if lang == "" {
		lang = "eng"
	}
	system := System()
	if system == nil {
		t.Skip(NotAvailable)
	}

	ctx := context.Background()
	subtitles, err := subs.ReadTrack(ctx, master, stream)
	if err != nil {
		t.Fatal(err)
	}

	if dir := os.Getenv("ARFABIT_OCR_PICTURES"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, s := range subtitles {
			d := s.Start
			name := fmt.Sprintf("%02d-%02d-%02d.%03d.png", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60, int(d.Milliseconds())%1000)
			if err := writePNG(filepath.Join(dir, name), s.Image); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("%d pictures saved to %s", len(subtitles), dir)
	}

	start := time.Now()
	result, err := ReadSubtitles(ctx, system, subtitles, lang, t.TempDir(), func(done, total int) {
		t.Logf("%d of %d read, %s", done, total, time.Since(start).Round(time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d pictures, %d lines read, in %s", len(subtitles), len(result.Cues), time.Since(start).Round(time.Second))

	t.Logf("%d of low confidence:", len(result.LowConfidence))
	for _, l := range result.LowConfidence {
		t.Logf("  %s  %q  (fast: %q)", l.Start.Round(time.Millisecond), l.Text, l.Fast)
	}

	if out := os.Getenv("ARFABIT_OCR_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(subs.WriteSRT(result.Cues)), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("written to %s", out)
	}
}

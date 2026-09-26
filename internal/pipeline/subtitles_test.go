package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/ocr"
	"github.com/arfabit/arfabit/internal/store"
)

// readsInOrder is a reader that reads each picture as the next line.
type readsInOrder struct{ n int }

func (r *readsInOrder) Read(_ context.Context, pictures []string, _ string) ([]ocr.Line, error) {
	lines := make([]ocr.Line, len(pictures))
	for i := range pictures {
		r.n++
		text := fmt.Sprintf("Line %d", r.n)
		lines[i] = ocr.Line{Text: text, Fast: text}
	}
	return lines, nil
}

// pgsMaster makes an original with a picture, keyframes every second, and a
// Blu-ray subtitle track of two lines: 1.0–2.0s and 3.5–4.5s.
func pgsMaster(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	var sup bytes.Buffer
	segment := func(typ byte, at float64, payload []byte) {
		var h [13]byte
		h[0], h[1] = 'P', 'G'
		binary.BigEndian.PutUint32(h[2:6], uint32(at*90000))
		h[10] = typ
		binary.BigEndian.PutUint16(h[11:13], uint16(len(payload)))
		sup.Write(h[:])
		sup.Write(payload)
	}
	for _, line := range []struct{ from, to float64 }{{1, 2}, {3.5, 4.5}} {
		segment(0x14, line.from, []byte{0, 0, 1, 0xFF, 0x80, 0x80, 0xFF}) // palette: index 1 white
		composition := make([]byte, 19)
		composition[10] = 1 // one picture
		segment(0x16, line.from, composition)
		object := make([]byte, 11)
		binary.BigEndian.PutUint16(object[7:9], 8)
		binary.BigEndian.PutUint16(object[9:11], 4)
		for range 4 {
			object = append(object, 0, 0x88, 1, 0, 0) // eight white pixels, end of line
		}
		segment(0x15, line.from, object)
		segment(0x80, line.from, nil)
		segment(0x16, line.to, make([]byte, 11)) // cleared
		segment(0x80, line.to, nil)
	}

	dir := t.TempDir()
	supPath := filepath.Join(dir, "subs.sup")
	if err := os.WriteFile(supPath, sup.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "original.mkv")
	// -copyts keeps the lines where they are: otherwise ffmpeg starts the
	// track at its first line, as no original from a disc would.
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=6",
		"-copyts", "-i", supPath,
		"-map", "0", "-map", "1", "-c:v", "libx264", "-g", "24", "-c:s", "copy",
		"-metadata:s:s:0", "language=eng", original).CombinedOutput(); err != nil {
		t.Fatalf("could not make an original: %v\n%s", err, out)
	}
	return original
}

func runPackageToEnd(t *testing.T, r *Runner, req ProjectRequest) *Job {
	t.Helper()
	job, err := r.StartProject(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for len(r.Active()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if job.State != store.StateDone {
		t.Fatalf("the package ended as %s: %s\n%s", job.State, job.Note, job.Detail)
	}
	return job
}

var convertSubtitles = []store.Item{
	{Kind: store.KindVideo, Source: 0, Action: store.ActionCopy},
	{Kind: store.KindSubtitle, Source: 1, Action: store.ActionConvert, To: "srt", Codec: "hdmv_pgs_subtitle", Lang: "eng"},
}

// A film's picture subtitles are read into text once it is made, and left
// beside it as a sidecar named as Plex reads it. They are not put in the
// file. Nothing ARFABIT made along the way stays behind.
func TestFilmSubtitlesAreReadIntoText(t *testing.T) {
	original := pgsMaster(t)
	r := runnerWithFolders(t)
	r.OCR = &readsInOrder{}

	job := runPackageToEnd(t, r, ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{Edition: "Text", Items: convertSubtitles},
	})

	folder := filepath.Dir(job.Delivery)
	sidecar := filepath.Join(folder, "Test Film (2026) {edition-Text}.en.srt")
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("no sidecar: %v", err)
	}
	want := "1\n00:00:01,000 --> 00:00:02,000\nLine 1\n\n2\n00:00:03,500 --> 00:00:04,500\nLine 2\n\n"
	if string(data) != want {
		t.Errorf("sidecar:\n%s\nwant:\n%s", data, want)
	}
	if len(job.Sidecars) != 1 || job.Sidecars[0] != sidecar {
		t.Errorf("sidecars = %v", job.Sidecars)
	}

	info, err := ffmpeg.Probe(context.Background(), job.Delivery)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range info.Streams {
		if s.Kind == "subtitle" {
			t.Errorf("the film holds a subtitle track (%s); read subtitles go beside it", s.Codec)
		}
	}
	entries, _ := os.ReadDir(folder)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".arfabit") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// A clip's subtitles are read from the piece it is made from, so they keep
// time with its picture. The piece starts at the keyframe before the time
// asked for — here 3.0s for 3.2s — so the line at 3.5s is 0.5s in; shifting
// the original's times by 3.2s would have put it at 0.3s.
func TestClipSubtitlesKeepTimeWithThePicture(t *testing.T) {
	original := pgsMaster(t)
	r := runnerWithFolders(t)
	r.OCR = &readsInOrder{}

	job := runPackageToEnd(t, r, ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{At: 3200 * time.Millisecond, Length: 2 * time.Second, Items: convertSubtitles},
	})

	clip := job.Made[0]
	sidecar := strings.TrimSuffix(clip, ".mkv") + ".en.srt"
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("no sidecar beside the clip: %v", err)
	}
	if want := "1\n00:00:00,500 --> 00:00:01,500\nLine 1\n\n"; string(data) != want {
		t.Errorf("the clip's subtitles:\n%s\nwant:\n%s", data, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(clip))
	if len(entries) != 2 {
		t.Errorf("the clip's folder holds %d files, want the clip and its subtitles", len(entries))
	}
}

// disagrees reads each picture as readsInOrder does, but its second reading
// of the second picture says other words.
type disagrees struct{ readsInOrder }

func (r *disagrees) Read(ctx context.Context, pictures []string, lang string) ([]ocr.Line, error) {
	lines, err := r.readsInOrder.Read(ctx, pictures, lang)
	if len(lines) == 2 {
		lines[1].Fast = "Lime 2"
	}
	return lines, err
}

// A subtitle of low confidence is listed on the job with both readings and
// its picture; the sidecar keeps the first reading.
func TestLowConfidenceIsListedOnTheJob(t *testing.T) {
	original := pgsMaster(t)
	r := runnerWithFolders(t)
	r.OCR = &disagrees{}

	job := runPackageToEnd(t, r, ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{Items: convertSubtitles},
	})

	if len(job.LowConfidence) != 1 {
		t.Fatalf("low confidence = %+v, want one", job.LowConfidence)
	}
	l := job.LowConfidence[0]
	if l.Text != "Line 2" || l.Fast != "Lime 2" || l.Start != 3500*time.Millisecond || l.Sidecar != job.Sidecars[0] {
		t.Errorf("entry = %+v", l)
	}
	if _, err := os.Stat(l.Picture); err != nil {
		t.Errorf("picture not kept: %v", err)
	}
	if !strings.Contains(job.Note, "One subtitle has low confidence") {
		t.Errorf("note = %q", job.Note)
	}
	data, _ := os.ReadFile(job.Sidecars[0])
	if !strings.Contains(string(data), "Line 2") {
		t.Errorf("the sidecar does not hold the first reading:\n%s", data)
	}
}

// Subtitles that cannot be read do not cost the film: it is made and
// delivered, and the note says plainly why there is no subtitle file.
func TestUnreadableSubtitlesDoNotStopTheFilm(t *testing.T) {
	original := pgsMaster(t)
	r := runnerWithFolders(t)
	message := "This Mac cannot read English text, so English subtitles can only be copied as they are."
	r.OCR = readerFunc(func(context.Context, []string, string) ([]ocr.Line, error) {
		return nil, &ocr.LanguageError{Lang: "eng", Message: message}
	})

	job := runPackageToEnd(t, r, ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{Items: convertSubtitles},
	})
	if !strings.Contains(job.Note, message) {
		t.Errorf("note = %q", job.Note)
	}
	if _, err := os.Stat(job.Delivery); err != nil {
		t.Errorf("the film was not made: %v", err)
	}
	if len(job.Sidecars) != 0 {
		t.Errorf("sidecars = %v", job.Sidecars)
	}
}

type readerFunc func(context.Context, []string, string) ([]ocr.Line, error)

func (f readerFunc) Read(ctx context.Context, p []string, l string) ([]ocr.Line, error) {
	return f(ctx, p, l)
}

// Where subtitles cannot be read, converting them is refused before anything
// joins the line; only Blu-ray picture subtitles are read; and only one track
// per language, since each becomes a file named by its language.
func TestSubtitleConversionIsChecked(t *testing.T) {
	video := store.Item{Kind: store.KindVideo, Action: store.ActionCopy}
	sub := store.Item{Kind: store.KindSubtitle, Action: store.ActionConvert, To: "srt", Codec: "hdmv_pgs_subtitle", Lang: "eng"}

	p := store.Project{Items: []store.Item{video, sub}}
	if err := checkProject(&p, false); err == nil || !strings.Contains(err.Error(), "cannot be read into text on this computer") {
		t.Errorf("no reader: err = %v", err)
	}
	if err := checkProject(&p, true); err != nil {
		t.Errorf("with a reader: %v", err)
	}

	dvd := sub
	dvd.Codec = "dvd_subtitle"
	p = store.Project{Items: []store.Item{video, dvd}}
	if err := checkProject(&p, true); err == nil || !strings.Contains(err.Error(), "Blu-ray") {
		t.Errorf("DVD subtitles: err = %v", err)
	}

	second := sub
	second.Source = 9
	p = store.Project{Items: []store.Item{video, sub, second}}
	if err := checkProject(&p, true); err == nil || !strings.Contains(err.Error(), "only one English") {
		t.Errorf("two English: err = %v", err)
	}
}

// Sidecars are named after the file they belong to and their language.
func TestSidecarPaths(t *testing.T) {
	pkg := &store.Project{Items: []store.Item{
		{Kind: store.KindSubtitle, Source: 3, Action: store.ActionConvert, Lang: "eng"},
		{Kind: store.KindSubtitle, Source: 5, Action: store.ActionConvert, Lang: "fre"},
		{Kind: store.KindSubtitle, Source: 6, Action: store.ActionCopy, Lang: "ger"},
	}}
	got := sidecarPaths(pkg, filepath.Join("lib", "Film (2020)", "Film (2020).mkv"))
	want := map[int]string{
		3: filepath.Join("lib", "Film (2020)", "Film (2020).en.srt"),
		5: filepath.Join("lib", "Film (2020)", "Film (2020).fr.srt"),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sidecars = %v, want %v", got, want)
	}
}

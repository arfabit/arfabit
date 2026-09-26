package ocr

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/subs"
)

// fake reads every picture as its own file name, or as what answers says.
type fake struct {
	answers map[int]Line
	calls   int
	seen    []string
	lang    string
}

func (f *fake) Read(ctx context.Context, pictures []string, lang string) ([]Line, error) {
	f.calls++
	f.lang = lang
	lines := make([]Line, len(pictures))
	for i, p := range pictures {
		if _, err := os.Stat(p); err != nil {
			return nil, err
		}
		f.seen = append(f.seen, p)
		n := len(f.seen) - 1
		if a, ok := f.answers[n]; ok {
			lines[i] = a
			continue
		}
		lines[i] = Line{Text: " line " + strings.TrimSuffix(filepath.Base(p), ".png") + " "}
	}
	return lines, nil
}

func subtitlesOf(n int) []subs.Subtitle {
	out := make([]subs.Subtitle, n)
	for i := range out {
		out[i] = subs.Subtitle{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i)*time.Second + 500*time.Millisecond,
			Image: image.NewGray(image.Rect(0, 0, 40, 20)),
		}
	}
	return out
}

// Pictures are read a chunk at a time, in order, with progress after each,
// and nothing ARFABIT wrote for the reader stays behind.
func TestReadSubtitlesInChunks(t *testing.T) {
	work := t.TempDir()
	r := &fake{}
	var progress []int
	got, err := ReadSubtitles(context.Background(), r, subtitlesOf(250), "fra", work,
		func(done, total int) {
			if total != 250 {
				t.Errorf("total = %d", total)
			}
			progress = append(progress, done)
		})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 3 || len(progress) != 3 || progress[2] != 250 {
		t.Errorf("calls = %d, progress = %v; want three chunks ending at 250", r.calls, progress)
	}
	if r.lang != "fra" {
		t.Errorf("language = %q, want the track's", r.lang)
	}
	if len(got.Cues) != 250 || got.Cues[120].Text != "line 00120" || got.Cues[120].Start != 120*time.Second {
		t.Errorf("cue 120 = %+v", got.Cues[120])
	}
	if len(got.LowConfidence) != 0 {
		t.Errorf("low confidence = %v, want none", got.LowConfidence)
	}
	if left, _ := os.ReadDir(work); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// A subtitle is of low confidence when nothing was read, when what was read
// holds a known misreading, or when the second reading says other words. It
// keeps what the first reading said (§15); one with nothing read is left out
// of the cues, never passed off as read.
func TestLowConfidence(t *testing.T) {
	r := &fake{answers: map[int]Line{
		0: {Text: "So do I.", Fast: "Sodol."},                             // other words
		1: {Text: "'Course | am.", Fast: "'Course l am."},                 // a "|"
		2: {Text: "", Fast: ""},                                           // nothing read
		3: {Text: "Why is Rebecca lying?", Fast: "Why is Rebecca lying."}, // punctuation only
		4: {Text: "And so am I.", Fast: "And so am l."},                   // a lone l for I
		5: {Text: "see everything.", Fast: "I see everything."},           // an I the first dropped
		6: {Text: "Fine", Fast: ""},                                       // no second reading
	}}
	got, err := ReadSubtitles(context.Background(), r, subtitlesOf(7), "eng", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cues) != 6 {
		t.Errorf("cues = %d, want 6: the empty one left out", len(got.Cues))
	}
	var at []int
	for _, l := range got.LowConfidence {
		at = append(at, l.Index)
	}
	if fmt.Sprint(at) != "[0 1 2 5]" {
		t.Errorf("low confidence at %v, want [0 1 2 5]", at)
	}
	if l := got.LowConfidence[0]; l.Text != "So do I." || l.Fast != "Sodol." || l.Start != 0 || l.End != 500*time.Millisecond {
		t.Errorf("first = %+v", l)
	}
}

// A reader's error stops the reading, as it is.
func TestReaderErrorIsReturned(t *testing.T) {
	r := readerFunc(func(context.Context, []string, string) ([]Line, error) {
		return nil, &LanguageError{Lang: "swe", Message: "no Swedish"}
	})
	_, err := ReadSubtitles(context.Background(), r, subtitlesOf(3), "swe", t.TempDir(), nil)
	var lang *LanguageError
	if !errors.As(err, &lang) || lang.Lang != "swe" {
		t.Errorf("err = %v", err)
	}
}

type readerFunc func(context.Context, []string, string) ([]Line, error)

func (f readerFunc) Read(ctx context.Context, p []string, l string) ([]Line, error) {
	return f(ctx, p, l)
}

// A clip keeps what shows within it, cut at its end.
func TestTrim(t *testing.T) {
	s := time.Second
	got := Trim([]subs.Cue{
		{Start: -2 * s, End: -1 * s, Text: "before"},
		{Start: -1 * s, End: 1 * s, Text: "across the start"},
		{Start: 3 * s, End: 4 * s, Text: "inside"},
		{Start: 9 * s, End: 12 * s, Text: "across the end"},
		{Start: 10 * s, End: 11 * s, Text: "after"},
	}, 10*s)
	want := []subs.Cue{
		{Start: 0, End: 1 * s, Text: "across the start"},
		{Start: 3 * s, End: 4 * s, Text: "inside"},
		{Start: 9 * s, End: 10 * s, Text: "across the end"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cue %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Windows PowerShell may start its answer with a byte order mark.
func TestReadAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(path, []byte("\uFEFF"+`{"lines":[{"text":"Bonjour\nà demain","fast":"Bonjour"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := readAnswer(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Lines) != 1 || a.Lines[0].Text != "Bonjour\nà demain" || a.Lines[0].Fast != "Bonjour" {
		t.Errorf("answer = %+v", a)
	}
}

// A script is given the list, the answer file and the language's short tag,
// and its answer is read back; a language it cannot read is said so plainly.
func TestScriptReader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	pictures := []string{filepath.Join(dir, "00000.png"), filepath.Join(dir, "00001.png")}

	var gotTag string
	s := script{
		name: "reader.sh",
		body: []byte(`if [ "$3" = sv ]; then echo '{"missing":true}' > "$2"; exit 0; fi
n=$(wc -l < "$1" | tr -d ' ')
echo "{\"lines\":[{\"text\":\"$n pictures\"},{\"text\":\"$3\"}]}" > "$2"
`),
		command: func(script, list, out, tag string) (string, []string) {
			gotTag = tag
			return "/bin/sh", []string{script, list, out, tag}
		},
		missing: func(name string) string { return "cannot read " + name },
	}

	lines, err := s.Read(context.Background(), pictures, "fre")
	if err != nil {
		t.Fatal(err)
	}
	if gotTag != "fr" || len(lines) != 2 || lines[0].Text != "2 pictures" || lines[1].Text != "fr" {
		t.Errorf("tag %q, lines %+v", gotTag, lines)
	}

	_, err = s.Read(context.Background(), pictures, "swe")
	var lang *LanguageError
	if !errors.As(err, &lang) || lang.Message != "cannot read Swedish" {
		t.Errorf("err = %v, want Swedish named", err)
	}

	// A reader that fails shows what it said, as it is (§15).
	s.body = []byte("echo 'something went wrong' >&2; exit 3\n")
	if _, err := s.Read(context.Background(), pictures, "eng"); err == nil || !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("err = %v, want the reader's own words", err)
	}
}

func TestTag(t *testing.T) {
	for code, want := range map[string]string{"eng": "en", "GER": "de", "chi": "zh", "xyz": "xyz", "": ""} {
		if got := Tag(code); got != want {
			t.Errorf("Tag(%q) = %q, want %q", code, got, want)
		}
	}
}

// Tesseract's TSV, as it writes it for a list of three pictures: two lines on
// the first, nothing on the second, one word on the third.
const sampleTSV = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
	"1\t1\t0\t0\t0\t0\t0\t0\t400\t120\t-1\t\n" +
	"4\t1\t1\t1\t1\t0\t10\t10\t300\t40\t-1\t\n" +
	"5\t1\t1\t1\t1\t1\t10\t10\t100\t40\t96.5\tWhere\n" +
	"5\t1\t1\t1\t1\t2\t120\t10\t100\t40\t91\tare\n" +
	"5\t1\t1\t1\t2\t1\t10\t60\t100\t40\t42\tyou?\n" +
	"1\t2\t0\t0\t0\t0\t0\t0\t400\t120\t-1\t\n" +
	"1\t3\t0\t0\t0\t0\t0\t0\t400\t120\t-1\t\n" +
	"5\t3\t1\t1\t1\t1\t10\t10\t100\t40\t88\tNo.\n"

func TestParseTSV(t *testing.T) {
	lines, err := parseTSV(sampleTSV, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{{Text: "Where are\nyou?"}, {}, {Text: "No."}}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("page %d = %+v, want %+v", i+1, lines[i], want[i])
		}
	}
	if _, err := parseTSV(sampleTSV, 2); err == nil {
		t.Error("an answer for a page that was not asked for was accepted")
	}
}

// Tesseract is asked for the language data it has, and says plainly when it
// has none for the track's language.
func TestTesseractLanguages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tesseract")
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --list-langs ]; then printf 'List of available languages in \"/x\" (3):\\neng\\nchi_tra\\nosd\\n'; exit 0; fi\n" +
		"echo \"$@\" > " + log + "\n" +
		"printf 'level\\tpage_num\\tblock_num\\tpar_num\\tline_num\\tword_num\\tleft\\ttop\\twidth\\theight\\tconf\\ttext\\n5\\t1\\t1\\t1\\t1\\t1\\t0\\t0\\t1\\t1\\t90\\tHi\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := tesseract{bin: bin}
	pictures := []string{filepath.Join(dir, "00000.png")}

	lines, err := r.Read(context.Background(), pictures, "chi")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(log)
	if !strings.Contains(string(args), "-l chi_tra tsv") || len(lines) != 1 || lines[0].Text != "Hi" {
		t.Errorf("args %q, lines %+v", args, lines)
	}

	_, err = r.Read(context.Background(), pictures, "fre")
	var lang *LanguageError
	if !errors.As(err, &lang) || !strings.Contains(lang.Message, "no French language data") {
		t.Errorf("err = %v", err)
	}
}

// In English, a letter English does not use is a known misreading; the
// accents English borrows are not. Other languages are not judged this way.
func TestSuspect(t *testing.T) {
	for _, tc := range []struct {
		text, lang string
		want       bool
	}{
		{"'Course | am.", "eng", true},
		{"'Course | am.", "fra", true},
		{"Bertie Hollingshead was the real killeriại", "eng", true},
		{"A café, a naïve résumé, and Zoë.", "eng", false},
		{"Just words.", "eng", false},
		{"Il était une fois ại", "fra", false},
		{"Nothing to see", "", false},
	} {
		if got := suspect(tc.text, tc.lang); got != tc.want {
			t.Errorf("suspect(%q, %q) = %v, want %v", tc.text, tc.lang, got, tc.want)
		}
	}
}

// Pairs from In the Grey: accurate reading, then fast. Fast mode's own
// mistakes are not a disagreement; the accurate reading's are. ("So | see."
// is listed too, by suspect, not by this.)
func TestSameWordsOnARealFilm(t *testing.T) {
	fastsMistakes := [][2]string{
		{"Baker! Baker!", "Bakerl Bakerl"},
		{"Get out of there!\nThat's an order!", "Get out of therel\nThat's an orderl"},
		{"-SID: Move!\n-Move it!", "-SID: Movel\n-move it!"},
		{"Damn it, Baker! Come in!\nWhat are you doing?", "Damn it, Bakerl Come inl\nWhat are you doing."},
		{"MORENO: Movin'!", "MORENO: Movin'l"},
		{"And stupid is synonymous\nwith naïve.", "And stupid is synonymous\nwith nai've."},
		{"Buenos días, Captain Sensible.", "Buenos dias, Captain Sensible."},
		{"Señor Salazar. Rachel Wild.", "Senor Salazar. Rachel Wild."},
		{"to the rear of the café.", "to the rear of the cafe."},
		{"And naïve is what\nI want you to think I am.", "And nai've is what\nI want you to think l am."},
	}
	for _, p := range fastsMistakes {
		if !sameWords(p[0], p[1]) {
			t.Errorf("%q and %q were taken to disagree", p[0], p[1])
		}
	}

	accuratesMistakes := [][2]string{
		{"Have a wondertul day,\nMr Horowitz.", "Have a wonderful day,\nMr Horowitz."},
		{"125 scooter tor eggs", "125 scooter for eggs"},
		{"invisible out useful.", "invisible but useful."},
		{"Now I'II have his attention.", "Now I'll have his attention."},
		{"If you'll allow me, III just", "If you'll allow me, I'll just"},
		{"-What happened there?\n- can do better.", "-what happened there?\n-1 can do better."},
		{"' be there.", "I'll be there."},
		{"can do it for 7.5%.", "Icandoitfor7.5%."},
	}
	for _, p := range accuratesMistakes {
		if sameWords(p[0], p[1]) {
			t.Errorf("%q and %q were taken to agree", p[0], p[1])
		}
	}
}

// Package ocr reads picture subtitles into text with a reader the computer
// already has: Vision on macOS, Windows.Media.Ocr on Windows, and Tesseract
// elsewhere when somebody has installed it (§10). Without one, subtitles can
// only be copied as they are.
//
// The reader is an interface so that tests need no operating system.
package ocr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/arfabit/arfabit/internal/subs"
)

// Line is what was read from one picture.
type Line struct {
	// Text is every line of text in the picture, top to bottom, one per
	// line. Empty when nothing was read. It is what goes into the SRT.
	Text string `json:"text"`

	// Fast is a second reading, made a different way, where the reader has
	// one: Vision's fast mode. It is never used unless a person chooses it;
	// where it disagrees with Text, the line is of low confidence.
	Fast string `json:"fast,omitempty"`
}

// Reader reads the text in pictures.
type Reader interface {
	// Read reads each PNG in pictures, and returns one Line for each, in
	// the same order. lang is the track's language as the master names it
	// (ISO 639-2, such as "eng"), or empty when it is not known.
	Read(ctx context.Context, pictures []string, lang string) ([]Line, error)
}

// LanguageError is a language the reader on this computer cannot read.
type LanguageError struct {
	Lang string

	// Message says so in plain words, for the page and the log.
	Message string
}

func (e *LanguageError) Error() string { return e.Message }

// chunk is how many pictures are read at a time, so progress can be told
// between them.
const chunk = 100

// LowConfidence is a subtitle OCR may have read wrong, for a person to
// check: nothing was read from it, what was read holds a mark known to be a
// misreading, or the second reading disagrees with the first. "Low
// confidence" is ARFABIT's own measure; no reader's score goes into it.
type LowConfidence struct {
	// Index is the subtitle's place in the track, so its picture can be kept.
	Index int

	Start, End time.Duration

	// Text is what went into the SRT (empty when nothing was read), and
	// Fast the second reading, when there is one.
	Text, Fast string
}

// Result is a track read into text.
type Result struct {
	Cues          []subs.Cue
	LowConfidence []LowConfidence
}

// ReadSubtitles reads every subtitle's picture, a chunk at a time, calling
// progress after each chunk. The pictures are written to a folder of
// ARFABIT's own under work, which is removed afterwards.
func ReadSubtitles(ctx context.Context, r Reader, subtitles []subs.Subtitle, lang, work string, progress func(done, total int)) (Result, error) {
	dir, err := os.MkdirTemp(work, ".arfabit-ocr-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	var result Result
	for from := 0; from < len(subtitles); from += chunk {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		batch := subtitles[from:min(from+chunk, len(subtitles))]

		pictures := make([]string, len(batch))
		for i, s := range batch {
			pictures[i] = filepath.Join(dir, fmt.Sprintf("%05d.png", from+i))
			if err := writePNG(pictures[i], s.Image); err != nil {
				return Result{}, err
			}
		}

		lines, err := r.Read(ctx, pictures, lang)
		if err != nil {
			return Result{}, err
		}
		if len(lines) != len(batch) {
			return Result{}, fmt.Errorf("the reader gave %d answers for %d pictures", len(lines), len(batch))
		}

		for i, line := range lines {
			s := batch[i]
			text := strings.TrimSpace(line.Text)
			fast := strings.TrimSpace(line.Fast)
			if text == "" || suspect(text, lang) || (fast != "" && !sameWords(text, fast)) {
				result.LowConfidence = append(result.LowConfidence, LowConfidence{
					Index: from + i, Start: s.Start, End: s.End, Text: text, Fast: fast,
				})
			}
			if text != "" {
				result.Cues = append(result.Cues, subs.Cue{Start: s.Start, End: s.End, Text: text})
			}
		}

		for _, p := range pictures {
			_ = os.Remove(p)
		}
		if progress != nil {
			progress(from+len(batch), len(subtitles))
		}
	}
	return result, nil
}

// suspect reports a line with a mark readers have been seen to put in place
// of another. It is listed for a person to check, never changed: guessing
// the right letter is how a wrong one gets in quietly (§15).
//
// A "|" is almost never in subtitles, and Vision has read "I" as "|" on
// every film tried ("'Course | am."). In English, a letter English does not
// use is one too: Vision ended two lines of The Sheep Detectives with "ại"
// ("the real killeriại"). The accents English borrows ("café", "naïve") are
// not counted. Other languages have their own letters, and are left until a
// film shows what their misreadings look like.
func suspect(text, lang string) bool {
	if strings.Contains(text, "|") {
		return true
	}
	if l, ok := languageOf(lang); ok && l.tag == "en" {
		for _, r := range text {
			if unicode.IsLetter(r) && r > unicode.MaxASCII && !strings.ContainsRune(borrowed, r) {
				return true
			}
		}
	}
	return false
}

// borrowed are the accented letters English writes in words it took from
// other languages.
const borrowed = "àáâäçèéêëìíîïñòóôöùúûüÿæœÀÁÂÄÇÈÉÊËÌÍÎÏÑÒÓÔÖÙÚÛÜŸÆŒ"

// sameWords reports whether two readings say the same words. What fast mode
// gets wrong all the time, and a person would learn nothing from being shown,
// is left out: case, punctuation and spacing; a lone "l", "1" or "|" against
// "I"; an accent English borrows against the plain letter ("cafe", "nai've");
// and, word by word, an "!" read as "l" ("Bakerl"). On The Sheep Detectives
// this brought 205 disagreements down to 34, and on In the Grey 50 down to
// 26 (with the "|" line), most of them real misreadings in the first reading.
func sameWords(a, b string) bool {
	if words(a) == words(b) {
		return true
	}
	wa, wb := strings.Fields(a), strings.Fields(b)
	if len(wa) != len(wb) {
		return false
	}
	for i := range wa {
		if words(wa[i]) != words(wb[i]) && words(bangAsL(wa[i])) != words(bangAsL(wb[i])) {
			return false
		}
	}
	return true
}

func bangAsL(s string) string { return strings.ReplaceAll(s, "!", "l") }

func words(s string) string {
	s = loneI.ReplaceAllString(strings.ToLower(s), "${1}i${2}")
	var b strings.Builder
	for _, r := range s {
		if plain, ok := unaccented[r]; ok {
			b.WriteString(plain)
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// unaccented is each accented letter English borrows, as the plain letter.
var unaccented = func() map[rune]string {
	m := map[rune]string{'æ': "ae", 'œ': "oe"}
	for plain, accented := range map[string]string{
		"a": "àáâä", "c": "ç", "e": "èéêë", "i": "ìíîï", "n": "ñ", "o": "òóôö", "u": "ùúûü", "y": "ÿ",
	} {
		for _, r := range accented {
			m[r] = plain
		}
	}
	return m
}()

// loneI is an "l", "1" or "|" standing on its own, where "I" could be.
var loneI = regexp.MustCompile(`(^|[^\p{L}\p{N}])[l1|]([^\p{L}\p{N}]|$)`)

// Trim keeps what shows within the first length of a clip, cut short where
// it runs past the end.
func Trim(cues []subs.Cue, length time.Duration) []subs.Cue {
	var out []subs.Cue
	for _, c := range cues {
		if c.Start >= length || c.End <= 0 {
			continue
		}
		c.Start = max(c.Start, 0)
		c.End = min(c.End, length)
		out = append(out, c)
	}
	return out
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// answer is what the scripts write: a Line per picture, or that the language
// cannot be read here.
type answer struct {
	Missing bool   `json:"missing"`
	Lines   []Line `json:"lines"`
}

// readAnswer reads a script's answer file.
func readAnswer(path string) (answer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return answer{}, err
	}
	// Windows PowerShell may start a UTF-8 file with a byte order mark.
	data = []byte(strings.TrimPrefix(string(data), "\uFEFF"))
	var a answer
	if err := json.Unmarshal(data, &a); err != nil {
		return answer{}, fmt.Errorf("the reader's answer could not be read: %w", err)
	}
	return a, nil
}

// script is a reader that runs a script the operating system understands
// over each chunk of pictures. The script reads a list of the pictures and
// writes its answer as JSON to a file, which keeps the text away from the
// console's own idea of character sets.
type script struct {
	name string // the script's file name
	body []byte

	// command is the program to run, given the script, the list, the answer
	// file, and the language tag to read in ("" for the reader's default).
	command func(script, list, out, tag string) (string, []string)

	// missing says that a language cannot be read here. name is empty
	// when the track does not say its language and the reader's own
	// default could not be used either.
	missing func(name string) string
}

func (s script) Read(ctx context.Context, pictures []string, lang string) ([]Line, error) {
	if len(pictures) == 0 {
		return nil, nil
	}
	dir := filepath.Dir(pictures[0])

	l, known := languageOf(lang)
	tag := ""
	if known {
		tag = l.tag
	}

	path := filepath.Join(dir, s.name)
	if err := os.WriteFile(path, s.body, 0o644); err != nil {
		return nil, err
	}
	list := filepath.Join(dir, "pictures.txt")
	if err := os.WriteFile(list, []byte(strings.Join(pictures, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "answer.json")
	_ = os.Remove(out)

	bin, args := s.command(path, list, out, tag)
	output, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v\n%s", err, output)
	}
	a, err := readAnswer(out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the reader finished without an answer\n%s", output)
	}
	if err != nil {
		return nil, err
	}
	if a.Missing {
		name := l.name
		if !known {
			name = strings.ToUpper(lang)
		}
		return nil, &LanguageError{Lang: lang, Message: s.missing(name)}
	}
	return a.Lines, nil
}

package subs

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shapes builds a set of distinct marks and an alphabet naming them, so that
// matching can be tested without a real font.
func shapes(letters string) (map[rune]*image.Gray, Alphabet) {
	images := map[rune]*image.Gray{}
	alphabet := Alphabet{}

	for i, r := range letters {
		// Each letter gets a distinct pattern.
		img := draw(
			pattern(i, 0),
			pattern(i, 1),
		)
		images[r] = img

		glyph := SplitGlyphs(img)[0].Glyphs[0]
		alphabet[glyph.Key] = string(r)
	}
	return images, alphabet
}

// pattern builds a mark unique to the letter index.
//
// The top row spells the index in binary, which guarantees no two letters
// share a shape — the whole test depends on distinct letters being distinct.
func pattern(i, row int) string {
	bits := make([]byte, 6)
	bits[0] = '#' // a leading mark, so trimming cannot make two letters equal

	for b := 0; b < 5; b++ {
		on := i&(1<<b) != 0
		if row == 1 {
			on = !on
		}
		if on {
			bits[b+1] = '#'
		} else {
			bits[b+1] = ' '
		}
	}
	return string(bits)
}

// word builds a subtitle spelling a word out of the given letter images.
func word(images map[rune]*image.Gray, text string, start time.Duration) Subtitle {
	var width, height int
	for _, r := range text {
		img := images[r]
		width += img.Bounds().Dx() + 1
		if h := img.Bounds().Dy(); h > height {
			height = h
		}
	}

	canvas := image.NewGray(image.Rect(0, 0, width, height))
	for i := range canvas.Pix {
		canvas.Pix[i] = 0xFF
	}

	x := 0
	for _, r := range text {
		img := images[r]
		for y := 0; y < img.Bounds().Dy(); y++ {
			for dx := 0; dx < img.Bounds().Dx(); dx++ {
				canvas.SetGray(x+dx, y, img.GrayAt(dx, y))
			}
		}
		x += img.Bounds().Dx() + 1
	}

	return Subtitle{Start: start, End: start + time.Second, Image: canvas}
}

var testWords = WordList{"the": true, "and": true, "he": true, "cat": true, "hat": true}

// An alphabet that knows the shapes but spells nonsense is the wrong one, and
// only reading the words reveals it.
func TestChooseAlphabetPrefersSense(t *testing.T) {
	images, correct := shapes("thecand")

	subtitles := []Subtitle{
		word(images, "the", 0),
		word(images, "cat", 2*time.Second),
		word(images, "and", 4*time.Second),
		word(images, "hat", 6*time.Second),
	}

	// A rival alphabet knowing exactly the same shapes, but naming them wrongly.
	scrambled := Alphabet{}
	for key := range correct {
		scrambled[key] = "z"
	}

	known := []*AlphabetFile{
		{Name: "scrambled", Glyphs: scrambled},
		{Name: "correct", Glyphs: correct},
	}

	match, ok := ChooseAlphabet(subtitles, known, testWords)
	if !ok {
		t.Fatal("no alphabet was chosen")
	}
	if match.File.Name != "correct" {
		t.Errorf("chose %q; the scrambled alphabet knows the same shapes but spells nonsense", match.File.Name)
	}
	if !match.Good() {
		t.Errorf("the correct alphabet was not judged good enough: covered=%.2f sensible=%.2f",
			match.Covered, match.Sensible)
	}
}

// Delivering confidently wrong subtitles is worse than delivering none,
// because nobody checks subtitles that look fine.
func TestWrongAlphabetIsNotGoodEnough(t *testing.T) {
	images, correct := shapes("thecand")
	subtitles := []Subtitle{word(images, "the", 0), word(images, "cat", time.Second)}

	scrambled := Alphabet{}
	for key := range correct {
		scrambled[key] = "z"
	}

	match, ok := ChooseAlphabet(subtitles, []*AlphabetFile{{Name: "scrambled", Glyphs: scrambled}}, testWords)
	if !ok {
		t.Fatal("no match was returned")
	}
	if match.Good() {
		t.Error("an alphabet spelling nonsense was judged good enough to use unattended")
	}
}

// Coverage counts occurrences, not distinct shapes: missing a rare mark
// matters far less than missing a common one.
func TestCoverageIsWeightedByHowOftenShapesAppear(t *testing.T) {
	images, full := shapes("ab")

	var subtitles []Subtitle
	for i := 0; i < 20; i++ {
		subtitles = append(subtitles, word(images, "a", time.Duration(i)*time.Second))
	}
	subtitles = append(subtitles, word(images, "b", 100*time.Second))

	// An alphabet knowing only the common mark.
	clusters := ClusterGlyphs(subtitles)
	commonOnly := Alphabet{clusters[0].Key: full[clusters[0].Key]}

	covered := coverageOf(clusters, commonOnly)
	if covered < 0.9 {
		t.Errorf("coverage = %.2f; knowing the common shape should count for most of it", covered)
	}
}

func TestWordListHandlesEndings(t *testing.T) {
	words := WordList{"walk": true, "cat": true}

	for _, w := range []string{"walk", "walked", "walking", "cats", "cat's", "1982"} {
		if !words.Has(w) {
			t.Errorf("Has(%q) = false", w)
		}
	}
	if words.Has("qzzt") {
		t.Error("nonsense was accepted as a word")
	}
}

func TestSaveAndLoadAlphabets(t *testing.T) {
	dir := t.TempDir()

	file := &AlphabetFile{
		Name:    "Warner 1080p",
		Glyphs:  Alphabet{"abc123": "A"},
		Learned: time.Now(),
		Discs:   []string{"Blade Runner (1982)"},
	}
	if err := file.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadAlphabets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d alphabets, want 1", len(loaded))
	}
	if loaded[0].Name != "Warner 1080p" || loaded[0].Glyphs["abc123"] != "A" {
		t.Errorf("round trip lost data: %+v", loaded[0])
	}
}

// A damaged alphabet must not stop the others being read.
func TestDamagedAlphabetIsSkipped(t *testing.T) {
	dir := t.TempDir()

	good := &AlphabetFile{Name: "good", Glyphs: Alphabet{"k": "A"}}
	if err := good.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadAlphabets(dir)
	if err != nil {
		t.Fatalf("one damaged alphabet stopped the rest: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("loaded %d alphabets, want the one good one", len(loaded))
	}
}

// Nothing learned yet is an ordinary state, not an error.
func TestLoadAlphabetsWithNothingLearned(t *testing.T) {
	loaded, err := LoadAlphabets(filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil {
		t.Fatalf("a missing directory was treated as a problem: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded %d alphabets from nowhere", len(loaded))
	}
}

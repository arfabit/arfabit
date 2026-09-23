package subs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AlphabetFile is a saved set of glyph shapes and their meanings.
//
// Subtitle images are rendered when the disc is authored, so the same studio
// using the same font at the same size produces pixel-identical glyphs on
// every release. An alphabet is therefore not a per-disc thing: it belongs to
// an authoring house, and there are not many of those.
type AlphabetFile struct {
	// Name says whose it is, as far as anyone can tell: "1080p, Arial-like,
	// first seen on a Warner disc".
	Name string `json:"name"`

	// Glyphs maps a shape to the character it stands for.
	Glyphs Alphabet `json:"glyphs"`

	// Learned is when it was put together.
	Learned time.Time `json:"learned"`

	// Discs names a few releases it is known to read, which is the only real
	// evidence of what it covers.
	Discs []string `json:"discs,omitempty"`
}

// Match is how well an alphabet reads a particular disc.
type Match struct {
	File *AlphabetFile

	// Covered is the share of the disc's shapes the alphabet knows, 0 to 1.
	Covered float64

	// Sensible is the share of the words it produces that look like words,
	// 0 to 1. Coverage alone is not enough: an alphabet from a different font
	// can know most shapes and still spell nonsense.
	Sensible float64
}

// Score combines the two, weighted towards words making sense.
func (m Match) Score() float64 { return m.Covered*0.4 + m.Sensible*0.6 }

// Good reports whether a match is worth using without asking.
//
// The bar is high on purpose. Delivering a film with confidently wrong
// subtitles is worse than delivering one with none, because nobody checks
// subtitles that look fine.
func (m Match) Good() bool { return m.Covered > 0.98 && m.Sensible > 0.90 }

// ChooseAlphabet finds which known alphabet reads a disc.
//
// Every alphabet is tried and the results are judged, rather than trusting a
// label: an alphabet that knows the shapes but spells gibberish is the wrong
// one, and only reading the words reveals it.
func ChooseAlphabet(subtitles []Subtitle, known []*AlphabetFile, words WordList) (Match, bool) {
	clusters := ClusterGlyphs(subtitles)
	if len(clusters) == 0 {
		return Match{}, false
	}

	// Judge on a sample: a hundred subtitles say as much about whether an
	// alphabet fits as two thousand, and far quicker.
	sample := subtitles
	if len(sample) > 100 {
		sample = sample[:100]
	}

	var best Match
	for _, file := range known {
		match := Match{
			File:     file,
			Covered:  coverageOf(clusters, file.Glyphs),
			Sensible: sensibleShare(Recognise(sample, file.Glyphs), words),
		}
		if best.File == nil || match.Score() > best.Score() {
			best = match
		}
	}

	return best, best.File != nil
}

// coverageOf is the share of a disc's glyph occurrences the alphabet knows.
//
// Weighted by how often each shape appears, because missing a rare shape
// matters far less than missing a common one.
func coverageOf(clusters []Cluster, alphabet Alphabet) float64 {
	var known, total int
	for _, c := range clusters {
		total += c.Count
		if _, ok := alphabet[c.Key]; ok {
			known += c.Count
		}
	}
	if total == 0 {
		return 0
	}
	return float64(known) / float64(total)
}

// sensibleShare is the share of words produced that look like words.
func sensibleShare(cues []Cue, words WordList) float64 {
	if len(words) == 0 {
		// With nothing to compare against, the only honest answer is that
		// this cannot be judged.
		return 0
	}

	var looksRight, total int
	for _, cue := range cues {
		for _, word := range strings.Fields(cue.Text) {
			cleaned := strings.ToLower(strings.Trim(word, ".,!?\"'“”‘’:;-—()[]"))
			if cleaned == "" {
				continue
			}
			total++
			if words.Has(cleaned) {
				looksRight++
			}
		}
	}

	if total == 0 {
		return 0
	}
	return float64(looksRight) / float64(total)
}

// WordList is the vocabulary used to judge whether text reads as English.
type WordList map[string]bool

// Has reports whether a word is in the list, ignoring a trailing 's.
func (w WordList) Has(word string) bool {
	if w[word] {
		return true
	}
	// Numbers are always fine.
	if isNumber(word) {
		return true
	}
	// Possessives and plurals, which no short list carries in full.
	for _, suffix := range []string{"'s", "s", "ed", "ing"} {
		if trimmed := strings.TrimSuffix(word, suffix); trimmed != word && w[trimmed] {
			return true
		}
	}
	return false
}

func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// LoadAlphabets reads every saved alphabet from a directory.
//
// A missing directory is not a problem: it means nothing has been learned yet,
// and the disc will be read by whatever other means are available.
func LoadAlphabets(dir string) ([]*AlphabetFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var files []*AlphabetFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		var file AlphabetFile
		if err := json.Unmarshal(data, &file); err != nil {
			// A damaged alphabet is skipped rather than allowed to stop the
			// others being read.
			continue
		}
		if len(file.Glyphs) > 0 {
			files = append(files, &file)
		}
	}

	sort.Slice(files, func(a, b int) bool { return files[a].Name < files[b].Name })
	return files, nil
}

// Save writes an alphabet so that the next disc in the same font is free.
func (f *AlphabetFile) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, f.Name)

	if name == "" {
		name = fmt.Sprintf("alphabet-%d", f.Learned.Unix())
	}
	return os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0o644)
}

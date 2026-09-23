// Package meta names files the way media managers expect.
package meta

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// Title is a movie's identity, once confirmed.
type Title struct {
	Name string
	Year int
}

// String renders "Blade Runner (1982)", the form Plex, Infuse and Jellyfin
// all read.
func (t Title) String() string {
	if t.Year > 0 {
		return fmt.Sprintf("%s (%d)", t.Name, t.Year)
	}
	return t.Name
}

// FolderName is the directory a title's files live in.
func (t Title) FolderName() string {
	return sanitize(t.String())
}

// BaseName is the filename stem, including the edition tag.
//
// The edition tag is always written, derived from the profile name. It lets a
// re-encode at different settings sit beside the original as a selectable
// edition instead of replacing it, and makes clear which profile produced a
// file (§6).
func (t Title) BaseName(edition string) string {
	base := t.String()
	if edition != "" {
		base = fmt.Sprintf("%s {edition-%s}", base, edition)
	}
	return sanitize(base)
}

// VideoName is the finished file's name.
func (t Title) VideoName(edition string) string {
	return t.BaseName(edition) + ".mp4"
}

// SubtitleName is a sidecar's name.
//
// Plex matches a sidecar to a video by exact filename stem, and reads .forced
// and .sdh as flags, so those go between the stem and the language.
func (t Title) SubtitleName(edition, lang string, forced, sdh bool) string {
	var suffix strings.Builder
	suffix.WriteString(".")
	if lang == "" {
		lang = "und"
	}
	suffix.WriteString(lang)
	if sdh {
		suffix.WriteString(".sdh")
	}
	if forced {
		suffix.WriteString(".forced")
	}
	return t.BaseName(edition) + suffix.String() + ".srt"
}

// LibraryDir is where a title's finished files go.
func (t Title) LibraryDir(root string) string {
	return filepath.Join(root, t.FolderName())
}

// MasterDir is where a title's untouched rip goes.
func (t Title) MasterDir(root string) string {
	return filepath.Join(root, t.FolderName())
}

// Characters no common filesystem accepts, split by what reading them aloud
// suggests. A slash separates words, so it becomes a dash; a question mark is
// punctuation, so "What?" should read "What" rather than "What-".
const (
	reservedSeparators = `/\:`
	reservedDropped    = `*?"<>|`
)

// sanitize makes a string safe as a filename while keeping it readable.
//
// Curly braces and parentheses survive deliberately: they carry meaning to
// Plex. Colons become a dash rather than vanishing, because titles like
// "Alien: Resurrection" read wrong without something in their place.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, ": ", " - ")
	s = strings.ReplaceAll(s, ":", "-")

	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune(reservedSeparators, r):
			b.WriteRune('-')
		case strings.ContainsRune(reservedDropped, r), unicode.IsControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}

	// Trailing dots and spaces are legal on some systems and not others.
	return strings.TrimRight(strings.TrimSpace(b.String()), ". -")
}

// CleanDiscLabel turns a volume label into something a person would search for.
//
// Labels arrive as THE_SHEEP_DETECTIVES or CRIME_101, and occasionally carry a
// disc or format suffix that is not part of the title.
func CleanDiscLabel(label string) string {
	s := strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(label)
	s = strings.Join(strings.Fields(s), " ")

	// Drop trailing format and disc markers.
	for _, suffix := range []string{
		" BLU RAY", " BLURAY", " BD", " UHD", " 4K", " DVD",
		" DISC 1", " DISC 2", " D1", " D2",
	} {
		s = strings.TrimSuffix(strings.ToUpper(s), suffix)
	}

	// A stripped suffix can leave a dangling dash behind.
	return strings.Trim(titleCase(s), " -\u2013\u2014")
}

// smallWords stay lowercase inside a title, the way titles are normally set.
var smallWords = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "but": true,
	"by": true, "for": true, "in": true, "nor": true, "of": true, "on": true,
	"or": true, "the": true, "to": true, "vs": true, "with": true,
}

// titleCase capitalises a shouted disc label into something readable.
//
// The first and last words are always capitalised, whatever they are.
func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && i < len(words)-1 && smallWords[w] {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

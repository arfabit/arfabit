package disc

import (
	"testing"
	"time"
)

// title is a terse constructor for table tests.
func title(i int, d time.Duration, size int64) Title {
	return Title{Index: i, Duration: d, SizeBytes: size}
}

func TestSelectTitlesPicksLongest(t *testing.T) {
	titles := []Title{
		title(0, 5*time.Minute, 100),    // too short to be content
		title(1, 100*time.Minute, 3000), // the feature
		title(2, 20*time.Minute, 600),   // a bonus feature
	}

	sel := SelectTitles(titles)
	if sel.Feature != 1 {
		t.Errorf("Feature = %d, want 1", sel.Feature)
	}
	if len(sel.Extras) != 1 || sel.Extras[0] != 2 {
		t.Errorf("Extras = %v, want [2]", sel.Extras)
	}
	if sel.Obfuscated {
		t.Error("Obfuscated = true on an ordinary disc")
	}
}

// Titles shorter than a plausible episode are menus and stingers, not content.
func TestSelectTitlesIgnoresShortTitles(t *testing.T) {
	titles := []Title{
		title(0, 30*time.Second, 10),
		title(1, 2*time.Minute, 20),
	}

	sel := SelectTitles(titles)
	if sel.Feature != -1 {
		t.Errorf("Feature = %d, want -1", sel.Feature)
	}
	if sel.Reason == "" {
		t.Error("Reason is empty; the user needs to be told why nothing was selected")
	}
}

// Playlist obfuscation lists many titles of near-identical length to make the
// feature hard to identify. ARFABIT reports it rather than quietly guessing.
func TestSelectTitlesDetectsObfuscation(t *testing.T) {
	var titles []Title
	for i := 0; i < 8; i++ {
		// Within a second or two of each other, as real decoys are.
		titles = append(titles, title(i, 100*time.Minute+time.Duration(i)*time.Second, 3000))
	}

	sel := SelectTitles(titles)
	if !sel.Obfuscated {
		t.Error("Obfuscated = false on a disc with eight near-identical titles")
	}
	if sel.Feature < 0 {
		t.Error("no feature suggested; obfuscation should still offer a best guess")
	}
}

// Two similar titles is ordinary — a film and its alternate cut — and must not
// be reported as obfuscation.
func TestSelectTitlesAllowsAlternateCuts(t *testing.T) {
	titles := []Title{
		title(0, 100*time.Minute, 3000),
		title(1, 101*time.Minute, 3100),
	}

	if sel := SelectTitles(titles); sel.Obfuscated {
		t.Error("Obfuscated = true for two similar titles; that is an ordinary alternate cut")
	}
}

// Equal durations are broken by size, so the higher-bitrate copy wins.
func TestSelectTitlesBreaksTiesBySize(t *testing.T) {
	titles := []Title{
		title(0, 100*time.Minute, 2000),
		title(1, 100*time.Minute, 9000),
	}

	if sel := SelectTitles(titles); sel.Feature != 1 {
		t.Errorf("Feature = %d, want 1 (the larger of two equal-length titles)", sel.Feature)
	}
}

func TestSelectTitlesEmpty(t *testing.T) {
	if sel := SelectTitles(nil); sel.Feature != -1 {
		t.Errorf("Feature = %d, want -1 for a disc with no titles", sel.Feature)
	}
}

// The real disc: with the length filter off, MakeMKV listed sixteen titles of
// which only one is the movie.
func TestSelectTitlesOnRealDisc(t *testing.T) {
	// Durations and sizes as parsed from testdata/scan-bd-1-minlen0.txt.
	titles := []Title{
		title(0, 109*time.Minute+4*time.Second, 33457569792),
	}
	for i := 1; i < 16; i++ {
		titles = append(titles, title(i, 20*time.Second, 5_000_000))
	}

	sel := SelectTitles(titles)
	if sel.Feature != 0 {
		t.Errorf("Feature = %d, want 0", sel.Feature)
	}
	if len(sel.Extras) != 0 {
		t.Errorf("Extras = %v, want none; the other titles are menus and stingers", sel.Extras)
	}
	if sel.Obfuscated {
		t.Error("Obfuscated = true; this disc is not obfuscated")
	}
}

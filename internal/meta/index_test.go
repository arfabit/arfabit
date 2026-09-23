package meta

import (
	"strings"
	"testing"
)

// A few lines in IMDb's published shape, headings included.
const sampleDataset = "tconst\ttitleType\tprimaryTitle\toriginalTitle\tisAdult\tstartYear\tendYear\truntimeMinutes\tgenres\n" +
	"tt0001\tmovie\tBlade Runner\tBlade Runner\t0\t1982\t\\N\t117\tSci-Fi\n" +
	"tt0002\tmovie\tCrime 101\tCrime 101\t0\t2025\t\\N\t140\tCrime\n" +
	"tt0003\tmovie\tIn the Grey\tIn the Grey\t0\t2025\t\\N\t97\tAction\n" +
	"tt0004\tshort\tSome Short\tSome Short\t0\t1999\t\\N\t12\tShort\n" +
	"tt0005\ttvSeries\tA Series\tA Series\t0\t2020\t\\N\t45\tDrama\n" +
	"tt0006\tmovie\tCrime 101\tCrime 101\t0\t1974\t\\N\t96\tCrime\n"

func parsed(t *testing.T, includeTV bool) *Index {
	t.Helper()
	ix, err := parseDataset(strings.NewReader(sampleDataset), includeTV)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// Films only by default: shorts and series are the bulk of the file and none
// of them arrive on a movie disc.
func TestParseDatasetKeepsFilms(t *testing.T) {
	ix := parsed(t, false)
	if len(ix.Entries) != 4 {
		t.Fatalf("got %d entries, want the 4 films", len(ix.Entries))
	}
	for _, e := range ix.Entries {
		if e.Title == "Some Short" || e.Title == "A Series" {
			t.Errorf("kept %q, which is not a film", e.Title)
		}
	}
}

func TestParseDatasetCanIncludeTV(t *testing.T) {
	if got := len(parsed(t, true).Entries); got != 5 {
		t.Errorf("got %d entries with TV included, want 5", got)
	}
}

// The year is the whole point: a disc label never carries one.
func TestLookupFindsYear(t *testing.T) {
	ix := parsed(t, false)

	matches := ix.Lookup("IN_THE_GREY_BLU_RAY", 97, 3)
	if len(matches) == 0 {
		t.Fatal("no match for a disc whose name and length both fit")
	}
	best := matches[0]

	if best.Title.Name != "In the Grey" {
		t.Errorf("matched %q", best.Title.Name)
	}
	if best.Title.Year != 2025 {
		t.Errorf("year = %d, want 2025", best.Title.Year)
	}
	if !strings.Contains(best.Why, "length") {
		t.Errorf("the reason does not mention the length: %q", best.Why)
	}
}

// Runtime is what separates two films sharing a name, which is exactly the
// case a label alone cannot solve.
func TestLookupUsesRuntimeToChooseBetweenSameNames(t *testing.T) {
	ix := parsed(t, false)

	newer := ix.Lookup("CRIME_101", 140, 3)
	if len(newer) == 0 || newer[0].Title.Year != 2025 {
		t.Fatalf("140 minutes should find the 2025 film, got %+v", newer)
	}

	older := ix.Lookup("CRIME_101", 96, 3)
	if len(older) == 0 || older[0].Title.Year != 1974 {
		t.Fatalf("96 minutes should find the 1974 film, got %+v", older)
	}
}

// A shouted label with the format tacked on is the normal case.
func TestLookupHandlesRealDiscLabels(t *testing.T) {
	ix := parsed(t, false)
	for _, label := range []string{"BLADE_RUNNER", "BLADE RUNNER - BLU-RAY", "blade.runner"} {
		matches := ix.Lookup(label, 117, 3)
		if len(matches) == 0 || matches[0].Title.Name != "Blade Runner" {
			t.Errorf("label %q did not find Blade Runner: %+v", label, matches)
		}
	}
}

// A name that matches nothing must return nothing rather than the nearest
// unrelated film: a wrong confident answer is worse than none (§15).
func TestLookupReturnsNothingForAnUnknownDisc(t *testing.T) {
	ix := parsed(t, false)
	if matches := ix.Lookup("SOME_HOME_VIDEO_2011", 88, 3); len(matches) != 0 {
		t.Errorf("an unknown disc matched %+v", matches)
	}
}

// A wrong length lowers confidence rather than ruling a film out: discs and
// listings disagree for ordinary reasons.
func TestLookupScoresRuntimeDisagreement(t *testing.T) {
	ix := parsed(t, false)

	agreeing := ix.Lookup("BLADE_RUNNER", 117, 1)
	disagreeing := ix.Lookup("BLADE_RUNNER", 160, 1)

	if len(agreeing) == 0 || len(disagreeing) == 0 {
		t.Fatal("expected a match either way")
	}
	if !(agreeing[0].Score > disagreeing[0].Score) {
		t.Errorf("a matching length did not score higher: %v vs %v", agreeing[0].Score, disagreeing[0].Score)
	}
}

// With no index, lookup is simply empty, and the disc's own name is used.
func TestLookupWithoutAnIndex(t *testing.T) {
	var ix *Index
	if matches := ix.Lookup("ANYTHING", 100, 3); matches != nil {
		t.Errorf("a missing index returned %+v", matches)
	}
}

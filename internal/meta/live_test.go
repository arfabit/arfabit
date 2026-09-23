package meta

import (
	"compress/gzip"
	"os"
	"testing"
)

// The published file is the only thing that proves the column positions are
// right. It is read from a local copy when one is present, because the real
// download is 200 MB and no test should fetch that.
//
//	curl -r 0-20000000 https://datasets.imdbws.com/title.basics.tsv.gz -o /tmp/imdb-part.gz
func TestParseRealDataset(t *testing.T) {
	path := os.Getenv("ARFABIT_IMDB_SAMPLE")
	if path == "" {
		t.Skip("set ARFABIT_IMDB_SAMPLE to a copy of title.basics.tsv.gz")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	// A truncated copy ends mid-stream, which is expected and not a failure:
	// what matters is that the rows read so far came out right.
	ix, _ := parseDataset(gz, false)

	if len(ix.Entries) == 0 {
		t.Fatal("no films were read from the real file")
	}

	var withYear, withRuntime int
	for _, e := range ix.Entries {
		if e.Title == "" {
			t.Fatalf("a film came through with no title: %+v", e)
		}
		if e.Year > 1870 && e.Year < 2100 {
			withYear++
		}
		if e.Minutes > 0 {
			withRuntime++
		}
	}

	// If a column moved, titles would be years or years would be genres.
	if withYear < len(ix.Entries)/2 {
		t.Errorf("only %d of %d films have a plausible year; a column has moved",
			withYear, len(ix.Entries))
	}
	if withRuntime < len(ix.Entries)/4 {
		t.Errorf("only %d of %d films have a runtime; a column has moved",
			withRuntime, len(ix.Entries))
	}

	t.Logf("read %d films, %d with a year, %d with a runtime", len(ix.Entries), withYear, withRuntime)
}

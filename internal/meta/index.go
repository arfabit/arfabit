package meta

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// datasetURL is IMDb's published title list.
//
// IMDb publishes whole files only, with no incremental feed, so a refresh is a
// full download. That is why ARFABIT does not do it on a schedule: the name and
// year of a film already on your shelf do not change.
const datasetURL = "https://datasets.imdbws.com/title.basics.tsv.gz"

// Index is the offline list of films, used to confirm a disc's title and year
// without asking anything on the internet at rip time.
type Index struct {
	Entries []IndexEntry `json:"entries"`
	Built   time.Time    `json:"built"`
}

// IndexEntry is one film.
type IndexEntry struct {
	Title   string `json:"t"`
	Year    int    `json:"y"`
	Minutes int    `json:"m"`
}

// Match is a candidate for what a disc contains.
type Match struct {
	Title Title `json:"title"`

	// Score is how well it matched, 0 to 1. Shown as confidence, never used to
	// decide on the user's behalf.
	Score float64 `json:"score"`

	// Why says what agreed, in plain words.
	Why string `json:"why"`
}

// runtimeTolerance is how far a disc's length may differ from the listed one.
//
// Discs and listings disagree by a minute or two routinely: credits, logos, and
// where a listing chose to round.
const runtimeTolerance = 3

// Lookup finds the films that fit a disc.
//
// Two signals come off the disc, and together they are strong: the main
// feature's runtime narrows hundreds of thousands of films to a few hundred,
// and the volume label picks among those. Neither alone is enough.
func (ix *Index) Lookup(label string, minutes int, limit int) []Match {
	if ix == nil || len(ix.Entries) == 0 {
		return nil
	}

	cleaned := strings.ToLower(CleanDiscLabel(label))
	var matches []Match

	for _, e := range ix.Entries {
		runtimeFits := minutes > 0 && e.Minutes > 0 && abs(e.Minutes-minutes) <= runtimeTolerance
		similarity := titleSimilarity(cleaned, strings.ToLower(e.Title))

		// A title that does not resemble the label at all is not a candidate,
		// however well the runtime happens to line up.
		if similarity < 0.5 {
			continue
		}

		score := similarity
		why := "the name is close"
		if runtimeFits {
			score = similarity*0.6 + 0.4
			why = "the name is close and the length matches"
		} else if minutes > 0 && e.Minutes > 0 {
			score *= 0.5
			why = "the name is close but the length does not match"
		}

		matches = append(matches, Match{
			Title: Title{Name: e.Title, Year: e.Year},
			Score: score,
			Why:   why,
		})
	}

	sortMatches(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// titleSimilarity compares two titles, ignoring punctuation and word order.
func titleSimilarity(a, b string) float64 {
	wordsA, wordsB := significantWords(a), significantWords(b)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	inB := map[string]bool{}
	for _, w := range wordsB {
		inB[w] = true
	}

	var shared int
	for _, w := range wordsA {
		if inB[w] {
			shared++
		}
	}

	longer := len(wordsA)
	if len(wordsB) > longer {
		longer = len(wordsB)
	}
	return float64(shared) / float64(longer)
}

// significantWords splits a title into comparable words, dropping the small
// ones that carry no distinguishing weight.
func significantWords(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if !smallWords[w] {
			out = append(out, w)
		}
	}
	// A title made entirely of small words still needs something to compare.
	if len(out) == 0 {
		return strings.Fields(s)
	}
	return out
}

func sortMatches(matches []Match) {
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && betterMatch(matches[j], matches[j-1]); j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}
}

func betterMatch(a, b Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	// Among equals, the older film: re-releases and remakes list later.
	return a.Title.Year < b.Title.Year
}

// IndexPath is where the built index lives.
func IndexPath(dataDir string) string {
	return filepath.Join(dataDir, "titles.json")
}

// LoadIndex reads a built index, returning nil when there is none.
//
// A missing index is not an error: ARFABIT falls back to the disc's own name,
// which on many discs is already right.
func LoadIndex(dataDir string) (*Index, error) {
	data, err := os.ReadFile(IndexPath(dataDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fmt.Errorf("the film list could not be read: %w", err)
	}
	return &ix, nil
}

// BuildIndex downloads IMDb's list and keeps the parts ARFABIT needs.
//
// The download is about 200 MB because IMDb publishes the whole file; what is
// kept is roughly 60 MB, being films only and five fields of each.
func BuildIndex(dataDir string, includeTV bool, onProgress func(read int64)) (*Index, error) {
	resp, err := http.Get(datasetURL)
	if err != nil {
		return nil, fmt.Errorf("the film list could not be downloaded: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the film list could not be downloaded: the server said %s", resp.Status)
	}

	var reader io.Reader = resp.Body
	if onProgress != nil {
		reader = &countingReader{r: resp.Body, onRead: onProgress}
	}

	gz, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	ix, err := parseDataset(gz, includeTV)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	data, err := json.Marshal(ix)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(IndexPath(dataDir), data, 0o644); err != nil {
		return nil, err
	}

	return ix, nil
}

// Dataset columns, in the order IMDb publishes them.
const (
	colTitleType     = 1
	colPrimaryTitle  = 2
	colStartYear     = 5
	colRuntimeMinute = 7
	colCount         = 9
)

// parseDataset reads the tab-separated list, keeping films.
func parseDataset(r io.Reader, includeTV bool) (*Index, error) {
	ix := &Index{Built: time.Now()}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// The first line is the column headings.
	if !sc.Scan() {
		return nil, fmt.Errorf("the film list was empty")
	}

	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < colCount {
			continue
		}

		kind := fields[colTitleType]
		wanted := kind == "movie" || kind == "tvMovie"
		if includeTV {
			wanted = wanted || kind == "tvSeries" || kind == "tvMiniSeries"
		}
		if !wanted {
			continue
		}

		year, _ := strconv.Atoi(fields[colStartYear])
		minutes, _ := strconv.Atoi(fields[colRuntimeMinute])

		ix.Entries = append(ix.Entries, IndexEntry{
			Title:   fields[colPrimaryTitle],
			Year:    year,
			Minutes: minutes,
		})
	}

	return ix, sc.Err()
}

// countingReader reports how much has been read, for a progress line.
type countingReader struct {
	r      io.Reader
	total  int64
	onRead func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += int64(n)
	if c.onRead != nil {
		c.onRead(c.total)
	}
	return n, err
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Package lab says what a part of a source, made at some settings, would come
// to across the whole of it, so that choosing a quality is a matter of watching
// rather than guessing (§14).
package lab

import (
	"sort"
	"time"
)

// Clip is one part made from a source: what it was made as, and what that
// came to.
type Clip struct {
	// Name identifies what it was made as: its edition, or the blueprint.
	Name string `json:"name"`

	// Path is the file, for watching.
	Path string `json:"path"`

	// Size is what it came to.
	Size int64 `json:"size"`

	// Took is how long it took to make, which extrapolates to the whole.
	Took time.Duration `json:"took"`

	// Problem is whatever went wrong, kept whole.
	Problem string `json:"problem,omitempty"`
}

// Comparison presents the results side by side.
type Comparison struct {
	Clips []Row `json:"clips"`

	// FilmLength is how long the whole film is, for extrapolating.
	FilmLength time.Duration `json:"film_length"`
}

// Row is one setting's numbers, each as a share of the best.
type Row struct {
	Clip Clip `json:"clip"`

	// SizePerHour is what this setting would come to across a whole film.
	SizePerHour int64 `json:"size_per_hour"`

	// Whole is the size the whole film would come to.
	Whole int64 `json:"whole_film"`

	// EncodeTime is how long the whole film would take at this setting.
	EncodeTime time.Duration `json:"encode_time"`

	// SizeShare and TimeShare are percentages of the best result, where the
	// best is 100. Smaller is better for both, so the best is the smallest.
	SizeShare float64 `json:"size_share"`
	TimeShare float64 `json:"time_share"`
}

// Compare works out what each setting would mean for a whole film.
func Compare(clips []Clip, clipLength, filmLength time.Duration) Comparison {
	c := Comparison{FilmLength: filmLength}
	if clipLength <= 0 {
		return c
	}

	var smallestSize int64
	var quickest time.Duration

	rows := make([]Row, 0, len(clips))
	for _, clip := range clips {
		if clip.Problem != "" || clip.Size == 0 {
			rows = append(rows, Row{Clip: clip})
			continue
		}

		scale := filmLength.Seconds() / clipLength.Seconds()
		row := Row{
			Clip:        clip,
			SizePerHour: int64(float64(clip.Size) * (3600 / clipLength.Seconds())),
			Whole:       int64(float64(clip.Size) * scale),
			EncodeTime:  time.Duration(float64(clip.Took) * scale),
		}

		if smallestSize == 0 || row.Whole < smallestSize {
			smallestSize = row.Whole
		}
		if quickest == 0 || row.EncodeTime < quickest {
			quickest = row.EncodeTime
		}
		rows = append(rows, row)
	}

	for i := range rows {
		if rows[i].Whole > 0 && smallestSize > 0 {
			rows[i].SizeShare = float64(smallestSize) / float64(rows[i].Whole) * 100
		}
		if rows[i].EncodeTime > 0 && quickest > 0 {
			rows[i].TimeShare = float64(quickest) / float64(rows[i].EncodeTime) * 100
		}
	}

	sort.SliceStable(rows, func(a, b int) bool {
		return rows[a].Whole < rows[b].Whole
	})

	c.Clips = rows
	return c
}

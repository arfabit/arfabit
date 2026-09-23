package subs

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"sort"
)

// Glyph is one mark cut out of a subtitle image.
type Glyph struct {
	// Bounds is where it sat in the original image, which is what puts the
	// letters back in order and finds the spaces between words.
	Bounds image.Rectangle

	// Bitmap is the mark itself, trimmed to its own edges.
	Bitmap *image.Gray

	// Key identifies the shape. Identical shapes share a key, which is what
	// makes the clustering exact rather than approximate.
	Key string
}

// Line is a row of glyphs, in reading order.
type Line struct {
	Glyphs []Glyph
	Bounds image.Rectangle
}

// inkThreshold is how dark a pixel must be to count as part of a mark.
//
// Subtitles are rendered with anti-aliased edges, so a glyph fades into the
// background rather than stopping. Halfway is the least arbitrary place to
// draw the line.
const inkThreshold = 0x80

// SplitGlyphs cuts a subtitle image into individual marks, in reading order.
//
// A subtitle is one or two lines of text, so the image is divided into rows
// first and each row into marks. Doing it in that order keeps the letters in
// the order they are read, which no amount of cleverness recovers afterwards.
func SplitGlyphs(img *image.Gray) []Line {
	if img == nil {
		return nil
	}

	var lines []Line
	for _, rows := range findRows(img) {
		line := Line{Bounds: rows}
		for _, column := range findColumns(img, rows) {
			bitmap, bounds := trim(img, column)
			if bitmap == nil {
				continue
			}
			line.Glyphs = append(line.Glyphs, Glyph{
				Bounds: bounds,
				Bitmap: bitmap,
				Key:    keyOf(bitmap),
			})
		}
		if len(line.Glyphs) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// findRows locates the bands of the image that contain marks.
func findRows(img *image.Gray) []image.Rectangle {
	b := img.Bounds()

	var rows []image.Rectangle
	start := -1

	for y := b.Min.Y; y < b.Max.Y; y++ {
		if rowHasInk(img, y) {
			if start < 0 {
				start = y
			}
			continue
		}
		if start >= 0 {
			rows = append(rows, image.Rect(b.Min.X, start, b.Max.X, y))
			start = -1
		}
	}
	if start >= 0 {
		rows = append(rows, image.Rect(b.Min.X, start, b.Max.X, b.Max.Y))
	}
	return rows
}

// findColumns locates the marks within one row.
func findColumns(img *image.Gray, row image.Rectangle) []image.Rectangle {
	var columns []image.Rectangle
	start := -1

	for x := row.Min.X; x < row.Max.X; x++ {
		if columnHasInk(img, x, row) {
			if start < 0 {
				start = x
			}
			continue
		}
		if start >= 0 {
			columns = append(columns, image.Rect(start, row.Min.Y, x, row.Max.Y))
			start = -1
		}
	}
	if start >= 0 {
		columns = append(columns, image.Rect(start, row.Min.Y, row.Max.X, row.Max.Y))
	}
	return columns
}

func rowHasInk(img *image.Gray, y int) bool {
	b := img.Bounds()
	for x := b.Min.X; x < b.Max.X; x++ {
		if img.GrayAt(x, y).Y < inkThreshold {
			return true
		}
	}
	return false
}

func columnHasInk(img *image.Gray, x int, row image.Rectangle) bool {
	for y := row.Min.Y; y < row.Max.Y; y++ {
		if img.GrayAt(x, y).Y < inkThreshold {
			return true
		}
	}
	return false
}

// trim cuts a mark down to its own edges, so that the same letter at a
// different height on the line is still the same shape.
func trim(img *image.Gray, area image.Rectangle) (*image.Gray, image.Rectangle) {
	minX, minY := area.Max.X, area.Max.Y
	maxX, maxY := area.Min.X, area.Min.Y
	found := false

	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if img.GrayAt(x, y).Y >= inkThreshold {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x >= maxX {
				maxX = x + 1
			}
			if y >= maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return nil, image.Rectangle{}
	}

	bounds := image.Rect(minX, minY, maxX, maxY)
	out := image.NewGray(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			out.SetGray(x, y, img.GrayAt(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return out, bounds
}

// keyOf identifies a shape.
//
// The mark is reduced to ink or no ink before hashing, so that the same letter
// rendered with slightly different anti-aliasing still matches itself. Two
// marks with the same key are the same letter, which is what lets a whole film
// be recognised from a hundred-odd examples.
func keyOf(bitmap *image.Gray) string {
	b := bitmap.Bounds()

	bits := make([]byte, 0, 4+(b.Dx()*b.Dy()+7)/8)
	bits = append(bits, byte(b.Dx()>>8), byte(b.Dx()), byte(b.Dy()>>8), byte(b.Dy()))

	var current byte
	var filled int
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			current <<= 1
			if bitmap.GrayAt(x, y).Y < inkThreshold {
				current |= 1
			}
			filled++
			if filled == 8 {
				bits = append(bits, current)
				current, filled = 0, 0
			}
		}
	}
	if filled > 0 {
		bits = append(bits, current<<(8-filled))
	}

	sum := sha256.Sum256(bits)
	return hex.EncodeToString(sum[:12])
}

// Cluster groups every glyph in a film by shape.
//
// A feature has tens of thousands of glyphs and something like a hundred and
// fifty distinct shapes, because a disc uses one font throughout. Recognising
// those hundred and fifty is the whole job; everything else follows from it.
type Cluster struct {
	Key string

	// Example is one of the marks, for showing or recognising.
	Example *image.Gray

	// Count is how many times the shape appears across the film, which says
	// which shapes are worth getting right.
	Count int
}

// ClusterGlyphs collects the distinct shapes, commonest first.
func ClusterGlyphs(subtitles []Subtitle) []Cluster {
	byKey := map[string]*Cluster{}

	for _, s := range subtitles {
		for _, line := range SplitGlyphs(s.Image) {
			for _, g := range line.Glyphs {
				c := byKey[g.Key]
				if c == nil {
					c = &Cluster{Key: g.Key, Example: g.Bitmap}
					byKey[g.Key] = c
				}
				c.Count++
			}
		}
	}

	clusters := make([]Cluster, 0, len(byKey))
	for _, c := range byKey {
		clusters = append(clusters, *c)
	}

	sort.Slice(clusters, func(a, b int) bool {
		if clusters[a].Count != clusters[b].Count {
			return clusters[a].Count > clusters[b].Count
		}
		return clusters[a].Key < clusters[b].Key
	})

	return clusters
}

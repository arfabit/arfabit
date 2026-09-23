package subs

import (
	"image"
	"testing"
	"time"
)

// draw builds a small image from rows of text, where '#' is ink.
func draw(rows ...string) *image.Gray {
	height := len(rows)
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}

	img := image.NewGray(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y, row := range rows {
		for x, c := range row {
			if c == '#' {
				img.SetGray(x, y, image.NewGray(image.Rect(0, 0, 1, 1)).GrayAt(0, 0))
			}
		}
	}
	return img
}

func TestSplitGlyphsFindsMarksInOrder(t *testing.T) {
	// Three marks with a gap between each.
	img := draw(
		"#  ##  #",
		"#  ##  #",
	)

	lines := SplitGlyphs(img)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if got := len(lines[0].Glyphs); got != 3 {
		t.Fatalf("got %d glyphs, want 3", got)
	}

	// Reading order: left to right.
	for i := 1; i < len(lines[0].Glyphs); i++ {
		if lines[0].Glyphs[i].Bounds.Min.X <= lines[0].Glyphs[i-1].Bounds.Min.X {
			t.Error("glyphs are not in reading order")
		}
	}
}

// A subtitle is often two lines, and they must not be run together.
func TestSplitGlyphsSeparatesLines(t *testing.T) {
	img := draw(
		"##  ##",
		"##  ##",
		"      ",
		"##    ",
		"##    ",
	)

	lines := SplitGlyphs(img)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if len(lines[0].Glyphs) != 2 || len(lines[1].Glyphs) != 1 {
		t.Errorf("glyphs per line = %d, %d; want 2, 1",
			len(lines[0].Glyphs), len(lines[1].Glyphs))
	}
}

// The same letter higher or lower on the line is still the same letter, which
// only holds if each mark is trimmed to its own edges.
func TestSameShapeAtDifferentHeightsMatches(t *testing.T) {
	high := draw(
		"##",
		"##",
		"  ",
	)
	low := draw(
		"  ",
		"##",
		"##",
	)

	a := SplitGlyphs(high)[0].Glyphs[0]
	b := SplitGlyphs(low)[0].Glyphs[0]

	if a.Key != b.Key {
		t.Errorf("the same shape got different keys:\n %s\n %s", a.Key, b.Key)
	}
}

func TestDifferentShapesDoNotMatch(t *testing.T) {
	a := SplitGlyphs(draw("##", "##"))[0].Glyphs[0]
	b := SplitGlyphs(draw("##", "# "))[0].Glyphs[0]

	if a.Key == b.Key {
		t.Error("two different shapes share a key")
	}
}

// The whole approach rests on this: a film has tens of thousands of glyphs and
// only a hundred or so distinct shapes, because a disc uses one font.
func TestClusterCollapsesRepeatedShapes(t *testing.T) {
	letterA := draw("##", "# ")
	letterB := draw("##", " #")

	var subtitles []Subtitle
	for i := 0; i < 50; i++ {
		subtitles = append(subtitles,
			Subtitle{Start: time.Duration(i) * time.Second, Image: letterA},
			Subtitle{Start: time.Duration(i) * time.Second, Image: letterB},
		)
	}
	// One extra of the first, so the counts differ.
	subtitles = append(subtitles, Subtitle{Image: letterA})

	clusters := ClusterGlyphs(subtitles)
	if len(clusters) != 2 {
		t.Fatalf("101 glyphs collapsed to %d shapes, want 2", len(clusters))
	}

	// Commonest first, so the shapes worth getting right come first.
	if clusters[0].Count != 51 || clusters[1].Count != 50 {
		t.Errorf("counts = %d, %d; want 51, 50", clusters[0].Count, clusters[1].Count)
	}
	if clusters[0].Example == nil {
		t.Error("a cluster has no example to recognise")
	}
}

func TestSplitGlyphsHandlesAnEmptyImage(t *testing.T) {
	if got := SplitGlyphs(draw("    ", "    ")); len(got) != 0 {
		t.Errorf("found %d lines in a blank image", len(got))
	}
	if got := SplitGlyphs(nil); got != nil {
		t.Errorf("found lines in no image at all")
	}
}

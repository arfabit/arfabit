// Package subs turns a disc's picture subtitles into text.
//
// Blu-ray and DVD subtitles are not text: they are little images, one per
// line of dialogue, which is why showing them makes Plex convert the whole picture (§10).
// Reading them back into words is what this package does.
package subs

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"time"
)

// PGS segment types, from the Blu-ray presentation graphics format.
const (
	segPalette     = 0x14
	segObject      = 0x15
	segComposition = 0x16
	segWindow      = 0x17
	segEnd         = 0x80
)

// pgsClock is the tick rate of the timestamps in the stream.
const pgsClock = 90000

// Subtitle is one subtitle as it appears on screen.
type Subtitle struct {
	Start time.Duration
	End   time.Duration

	// Image is the rendered subtitle, with transparent background.
	Image *image.Gray

	// X and Y are where it sits on the screen, which matters for telling
	// dialogue from a caption at the top.
	X, Y int
}

// Duration is how long the subtitle is on screen.
func (s Subtitle) Duration() time.Duration { return s.End - s.Start }

// segment is one chunk of the stream.
type segment struct {
	typ  byte
	pts  time.Duration
	data []byte
}

// ParseSUP reads a .sup file, the form ffmpeg writes when copying a PGS
// subtitle track out of a disc.
//
// Damaged segments are skipped rather than treated as fatal: one unreadable
// subtitle in two thousand should not cost the other 1,999.
func ParseSUP(r io.Reader) ([]Subtitle, error) {
	var (
		subs    []Subtitle
		palette []color
		pending *Subtitle
	)

	for {
		seg, err := readSegment(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return subs, err
		}

		switch seg.typ {
		case segPalette:
			if p, err := parsePalette(seg.data); err == nil {
				palette = p
			}

		case segComposition:
			// A composition with no objects clears the screen, which ends
			// whatever was showing.
			objects, x, y, err := parseComposition(seg.data)
			if err != nil {
				continue
			}
			if objects == 0 {
				if pending != nil {
					pending.End = seg.pts
					subs = append(subs, *pending)
					pending = nil
				}
				continue
			}
			pending = &Subtitle{Start: seg.pts, X: x, Y: y}

		case segObject:
			if pending == nil {
				continue
			}
			img, err := parseObject(seg.data, palette)
			if err != nil {
				continue
			}
			pending.Image = img
		}
	}

	// A stream that ends while something is on screen still has one to give.
	if pending != nil && pending.Image != nil {
		pending.End = pending.Start + 3*time.Second
		subs = append(subs, *pending)
	}

	return withoutEmpties(subs), nil
}

// withoutEmpties drops compositions that carried no picture.
func withoutEmpties(subs []Subtitle) []Subtitle {
	out := subs[:0]
	for _, s := range subs {
		if s.Image != nil && s.End > s.Start {
			out = append(out, s)
		}
	}
	return out
}

// readSegment reads one segment header and its payload.
//
// In a .sup file each segment begins with the marker "PG"; inside a Matroska
// file the marker is absent, because the container already provides framing.
func readSegment(r io.Reader) (segment, error) {
	var header [13]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return segment{}, io.EOF
		}
		return segment{}, err
	}

	if header[0] != 'P' || header[1] != 'G' {
		return segment{}, fmt.Errorf("this does not look like a subtitle stream")
	}

	ticks := binary.BigEndian.Uint32(header[2:6])
	seg := segment{
		typ: header[10],
		pts: time.Duration(ticks) * time.Second / pgsClock,
	}

	size := binary.BigEndian.Uint16(header[11:13])
	if size > 0 {
		seg.data = make([]byte, size)
		if _, err := io.ReadFull(r, seg.data); err != nil {
			return segment{}, err
		}
	}

	return seg, nil
}

// parseComposition reads how many objects are on screen and where the first
// one sits.
func parseComposition(data []byte) (objects, x, y int, err error) {
	// width, height, frame rate, composition number, state, palette flag,
	// palette id, object count.
	const headerLen = 11
	if len(data) < headerLen {
		return 0, 0, 0, fmt.Errorf("composition is too short")
	}

	objects = int(data[10])
	if objects == 0 {
		return 0, 0, 0, nil
	}

	// Each object entry is at least eight bytes: id, window, flags, x, y.
	const entryLen = 8
	if len(data) < headerLen+entryLen {
		return objects, 0, 0, nil
	}

	x = int(binary.BigEndian.Uint16(data[headerLen+4 : headerLen+6]))
	y = int(binary.BigEndian.Uint16(data[headerLen+6 : headerLen+8]))
	return objects, x, y, nil
}

// color is one palette entry, kept as the grey level and opacity that matter
// for reading text.
type color struct {
	luma  uint8
	alpha uint8
}

// parsePalette reads the colour table.
//
// Only brightness and opacity are kept: subtitles are read as shapes, and the
// colour they happen to be printed in makes no difference to the words.
func parsePalette(data []byte) ([]color, error) {
	const (
		headerLen = 2 // palette id, version
		entryLen  = 5 // index, Y, Cr, Cb, alpha
	)
	if len(data) < headerLen {
		return nil, fmt.Errorf("palette is too short")
	}

	palette := make([]color, 256)
	for i := headerLen; i+entryLen <= len(data); i += entryLen {
		palette[data[i]] = color{luma: data[i+1], alpha: data[i+4]}
	}
	return palette, nil
}

// parseObject decodes one subtitle image.
func parseObject(data []byte, palette []color) (*image.Gray, error) {
	// object id, version, sequence flag, data length, width, height.
	const headerLen = 11
	if len(data) < headerLen {
		return nil, fmt.Errorf("object is too short")
	}

	width := int(binary.BigEndian.Uint16(data[7:9]))
	height := int(binary.BigEndian.Uint16(data[9:11]))
	if width <= 0 || height <= 0 || width > 4096 || height > 2160 {
		return nil, fmt.Errorf("object claims an impossible size of %dx%d", width, height)
	}

	return decodeRLE(data[headerLen:], width, height, palette)
}

// decodeRLE expands the run-length encoded image.
//
// The encoding is the one the Blu-ray specification defines: a zero byte
// introduces a run, and anything else is a single pixel.
func decodeRLE(data []byte, width, height int, palette []color) (*image.Gray, error) {
	img := image.NewGray(image.Rect(0, 0, width, height))

	// Text is read as dark-on-light, so the image starts white and the glyphs
	// are painted into it. Transparent areas stay white.
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}

	x, y := 0, 0
	set := func(count int, index byte) {
		c := color{}
		if int(index) < len(palette) {
			c = palette[index]
		}
		for i := 0; i < count && y < height; i++ {
			if x < width && c.alpha > 0x40 {
				// Opaque pixels become their brightness, inverted so that
				// bright text reads as dark marks on a light page.
				img.Pix[y*img.Stride+x] = 0xFF - c.luma
			}
			x++
			if x >= width {
				x = 0
				y++
			}
		}
	}

	for i := 0; i < len(data); {
		b := data[i]
		i++

		if b != 0 {
			set(1, b)
			continue
		}

		if i >= len(data) {
			break
		}

		flags := data[i]
		i++

		switch {
		case flags == 0:
			// End of line.
			if x > 0 {
				x = 0
				y++
			}

		case flags < 0x40:
			set(int(flags), 0)

		case flags < 0x80:
			if i >= len(data) {
				return img, nil
			}
			set(int(flags&0x3F)<<8|int(data[i]), 0)
			i++

		case flags < 0xC0:
			if i >= len(data) {
				return img, nil
			}
			set(int(flags&0x3F), data[i])
			i++

		default:
			if i+1 >= len(data) {
				return img, nil
			}
			set(int(flags&0x3F)<<8|int(data[i]), data[i+1])
			i += 2
		}
	}

	return img, nil
}

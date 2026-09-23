package subs

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// supWriter builds a subtitle stream by hand, so the parser can be tested
// without a Blu-ray to hand.
type supWriter struct {
	buf bytes.Buffer
}

func (w *supWriter) segment(typ byte, pts time.Duration, payload []byte) {
	var header [13]byte
	header[0], header[1] = 'P', 'G'

	ticks := uint32(pts * pgsClock / time.Second)
	binary.BigEndian.PutUint32(header[2:6], ticks)
	header[10] = typ
	binary.BigEndian.PutUint16(header[11:13], uint16(len(payload)))

	w.buf.Write(header[:])
	w.buf.Write(payload)
}

// palette with one visible entry: index 1 is opaque white.
func (w *supWriter) palette(pts time.Duration) {
	w.segment(segPalette, pts, []byte{
		0, 0, // palette id, version
		1, 0xFF, 0x80, 0x80, 0xFF, // index 1: bright, fully opaque
	})
}

// composition announcing one object at a position.
func (w *supWriter) composition(pts time.Duration, x, y int) {
	payload := make([]byte, 11+8)
	payload[10] = 1 // one object
	binary.BigEndian.PutUint16(payload[15:17], uint16(x))
	binary.BigEndian.PutUint16(payload[17:19], uint16(y))
	w.segment(segComposition, pts, payload)
}

// clear announces a composition with no objects, which ends the subtitle.
func (w *supWriter) clear(pts time.Duration) {
	w.segment(segComposition, pts, make([]byte, 11))
}

// object carries the image itself.
func (w *supWriter) object(pts time.Duration, width, height int, rle []byte) {
	payload := make([]byte, 11)
	binary.BigEndian.PutUint16(payload[7:9], uint16(width))
	binary.BigEndian.PutUint16(payload[9:11], uint16(height))
	w.segment(segObject, pts, append(payload, rle...))
}

func TestParseSUPReadsOneSubtitle(t *testing.T) {
	var w supWriter
	w.palette(0)
	w.composition(time.Second, 100, 900)
	// Four pixels of colour 1, then end of line; twice, for a 4x2 image.
	w.object(time.Second, 4, 2, []byte{
		0x00, 0x84, 0x01, 0x00, 0x00,
		0x00, 0x84, 0x01, 0x00, 0x00,
	})
	w.clear(3 * time.Second)

	subs, err := ParseSUP(&w.buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subtitles, want 1", len(subs))
	}

	s := subs[0]
	if s.Start != time.Second || s.End != 3*time.Second {
		t.Errorf("timing = %v to %v, want 1s to 3s", s.Start, s.End)
	}
	if s.Duration() != 2*time.Second {
		t.Errorf("Duration = %v", s.Duration())
	}
	if s.X != 100 || s.Y != 900 {
		t.Errorf("position = (%d,%d), want (100,900)", s.X, s.Y)
	}

	bounds := s.Image.Bounds()
	if bounds.Dx() != 4 || bounds.Dy() != 2 {
		t.Fatalf("image is %dx%d, want 4x2", bounds.Dx(), bounds.Dy())
	}
}

// Bright text becomes dark marks on a light page, which is what every reader
// of text expects and what recognition works best on.
func TestDecodeRLEInvertsForReading(t *testing.T) {
	palette := make([]color, 256)
	palette[1] = color{luma: 0xFF, alpha: 0xFF} // bright, opaque

	img, err := decodeRLE([]byte{0x00, 0x82, 0x01, 0x00, 0x00}, 4, 1, palette)
	if err != nil {
		t.Fatal(err)
	}

	// Two painted pixels, then two untouched.
	if got := img.Pix[0]; got != 0x00 {
		t.Errorf("painted pixel = %#x, want black", got)
	}
	if got := img.Pix[2]; got != 0xFF {
		t.Errorf("untouched pixel = %#x, want white", got)
	}
}

// Transparent pixels are background however bright their colour claims to be.
func TestDecodeRLEIgnoresTransparentPixels(t *testing.T) {
	palette := make([]color, 256)
	palette[1] = color{luma: 0xFF, alpha: 0x00} // bright but invisible

	img, err := decodeRLE([]byte{0x00, 0x84, 0x01}, 4, 1, palette)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range img.Pix {
		if p != 0xFF {
			t.Errorf("pixel %d = %#x; a transparent colour was painted", i, p)
		}
	}
}

// A run longer than 63 pixels uses the two-byte form.
func TestDecodeRLELongRuns(t *testing.T) {
	palette := make([]color, 256)
	palette[1] = color{luma: 0xFF, alpha: 0xFF}

	// 0xC0|1, 0x00 -> 256 pixels of colour 1.
	img, err := decodeRLE([]byte{0x00, 0xC1, 0x00, 0x01}, 256, 1, palette)
	if err != nil {
		t.Fatal(err)
	}

	var painted int
	for _, p := range img.Pix {
		if p == 0x00 {
			painted++
		}
	}
	if painted != 256 {
		t.Errorf("painted %d pixels, want 256", painted)
	}
}

// One unreadable subtitle in two thousand must not cost the other 1,999.
func TestParseSUPSkipsDamagedSegments(t *testing.T) {
	var w supWriter
	w.palette(0)

	// An object claiming an impossible size.
	w.composition(time.Second, 0, 0)
	w.segment(segObject, time.Second, []byte{0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF})
	w.clear(2 * time.Second)

	// A perfectly good one after it.
	w.composition(3*time.Second, 10, 20)
	w.object(3*time.Second, 2, 1, []byte{0x00, 0x82, 0x01, 0x00, 0x00})
	w.clear(5 * time.Second)

	subs, err := ParseSUP(&w.buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subtitles, want the one good subtitle", len(subs))
	}
	if subs[0].Start != 3*time.Second {
		t.Errorf("kept the wrong subtitle: starts at %v", subs[0].Start)
	}
}

func TestParseSUPRejectsSomethingElseEntirely(t *testing.T) {
	_, err := ParseSUP(bytes.NewReader([]byte("this is not a subtitle stream at all")))
	if err == nil {
		t.Error("nonsense was accepted as a subtitle stream")
	}
}

// Timestamps are in 90 kHz ticks.
func TestSegmentTiming(t *testing.T) {
	var w supWriter
	w.palette(90 * time.Second)

	seg, err := readSegment(&w.buf)
	if err != nil {
		t.Fatal(err)
	}
	if seg.pts != 90*time.Second {
		t.Errorf("pts = %v, want 90s", seg.pts)
	}
}

// Command icon makes ARFABIT's icons for Windows and Linux from the one
// picture exported from Icon Composer, packaging/icon/ARFABIT.png, which is
// the icon the Mac app is built from (shell/macos/icon/ARFABIT.icon).
//
//	go run ./packaging/icon -out dist/icons
//
// It writes:
//
//	ARFABIT.ico         the app's icon at every size Windows asks for
//	tray-light.ico      a plain disc, light, for a dark taskbar
//	tray-dark.ico       the same disc, dark, for a light taskbar
//	arfabit.png         256 pixels, for the Linux app menu entry
//
// The tray icons are drawn here rather than exported: at 16 pixels the full
// icon is a blur, and the Mac's menu bar shows a plain disc too.
//
// Only Go's own packages are used, so it runs wherever ARFABIT is built.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func main() {
	var (
		source = flag.String("source", "packaging/icon/ARFABIT.png", "the icon exported from Icon Composer, 1024 pixels square")
		out    = flag.String("out", "dist/icons", "folder to write the icons into")
	)
	flag.Parse()

	if err := run(*source, *out); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(source, out string) error {
	art, err := readPNG(source)
	if err != nil {
		return fmt.Errorf("%w\n\nExport the icon from Icon Composer (shell/macos/icon/ARFABIT.icon) "+
			"as a 1024 pixel PNG, and save it as %s.", err, source)
	}
	if b := art.Bounds(); b.Dx() != b.Dy() || b.Dx() < 256 {
		return fmt.Errorf("%s is %d by %d pixels; it needs to be square, and 1024 pixels is best", source, b.Dx(), b.Dy())
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	// Windows picks from these for the program, Start, Explorer and the
	// installer, at every display scale.
	var app []image.Image
	for _, size := range []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256} {
		app = append(app, scale(art, size))
	}
	if err := writeICO(filepath.Join(out, "ARFABIT.ico"), app); err != nil {
		return err
	}

	// The notification area, at 100% to 300% display scale.
	traySizes := []int{16, 20, 24, 32, 40, 48}
	for name, ink := range map[string]color.NRGBA{
		"tray-light.ico": {0xF2, 0xF2, 0xF2, 0xFF},
		"tray-dark.ico":  {0x1F, 0x1F, 0x1F, 0xFF},
	} {
		var tray []image.Image
		for _, size := range traySizes {
			tray = append(tray, disc(size, ink))
		}
		if err := writeICO(filepath.Join(out, name), tray); err != nil {
			return err
		}
	}

	return writePNG(filepath.Join(out, "arfabit.png"), scale(art, 256))
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// scale shrinks a square picture to size pixels, each the average of the
// part of the original it covers. Colours are averaged by how opaque they
// are, so edges do not darken.
func scale(src image.Image, size int) image.Image {
	b := src.Bounds()
	ratio := float64(b.Dx()) / float64(size)
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		y0, y1 := float64(y)*ratio, float64(y+1)*ratio
		for x := 0; x < size; x++ {
			x0, x1 := float64(x)*ratio, float64(x+1)*ratio

			var r, g, bl, a, area float64
			for sy := int(y0); float64(sy) < y1; sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); float64(sx) < x1; sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					w := wx * wy
					c := color.NRGBA64Model.Convert(src.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA64)
					ca := float64(c.A) / 0xffff
					r += float64(c.R) / 0xffff * ca * w
					g += float64(c.G) / 0xffff * ca * w
					bl += float64(c.B) / 0xffff * ca * w
					a += ca * w
					area += w
				}
			}

			if a > 0 {
				dst.SetNRGBA(x, y, color.NRGBA{
					R: byte(math.Round(r / a * 255)),
					G: byte(math.Round(g / a * 255)),
					B: byte(math.Round(bl / a * 255)),
					A: byte(math.Round(a / area * 255)),
				})
			}
		}
	}
	return dst
}

// disc draws a plain disc in one colour: a filled circle with a clear ring
// round its hub and a hole through the middle. Each pixel is sampled 8 by 8
// times, so the edges are smooth at the smallest sizes.
func disc(size int, ink color.NRGBA) image.Image {
	const n = 8
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	centre := float64(size) / 2
	outer := centre - 0.5

	// As fractions of the disc's radius. Below 32 pixels the ring round the
	// hub is too fine to see, so a small disc has only its hole, made a
	// little larger to be seen.
	detailed := size >= 32
	hole := 0.17
	if !detailed {
		hole = 0.24
	}
	inside := func(d float64) bool {
		f := d / outer
		switch {
		case f > 1:
			return false
		case f < hole:
			return false
		case detailed && f > 0.34 && f < 0.44:
			return false // the clear ring round the hub
		}
		return true
	}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					px := float64(x) + (float64(sx)+0.5)/n - centre
					py := float64(y) + (float64(sy)+0.5)/n - centre
					if inside(math.Hypot(px, py)) {
						hits++
					}
				}
			}
			if hits > 0 {
				c := ink
				c.A = byte(int(ink.A) * hits / (n * n))
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return img
}

// writeICO writes pictures into one .ico file, each stored as a PNG, which
// Windows has read since Vista.
func writeICO(path string, images []image.Image) error {
	var pictures [][]byte
	for _, img := range images {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		pictures = append(pictures, buf.Bytes())
	}

	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(images))}) // reserved, icon, count

	offset := 6 + 16*len(images)
	for i, img := range images {
		side := img.Bounds().Dx()
		dim := byte(side)
		if side >= 256 {
			dim = 0 // 0 means 256
		}
		out.Write([]byte{dim, dim, 0, 0})                    // width, height, colours, reserved
		_ = binary.Write(&out, le, [2]uint16{1, 32})         // planes, bits per pixel
		_ = binary.Write(&out, le, uint32(len(pictures[i]))) // size
		_ = binary.Write(&out, le, uint32(offset))           // where it starts
		offset += len(pictures[i])
	}
	for _, p := range pictures {
		out.Write(p)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

package ffmpeg

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// HDR is the static HDR10 metadata a disc carries.
//
// This is the highest-consequence data in the pipeline. An HDR source encoded
// without it produces a file that is technically valid and plays back washed
// out and grey, with no error anywhere. See docs/ARCHITECTURE.md §9.
type HDR struct {
	// Display primaries and white point, in units of 0.00002, as x265 wants.
	GreenX, GreenY int
	BlueX, BlueY   int
	RedX, RedY     int
	WhiteX, WhiteY int

	// Luminance in units of 0.0001 cd/m².
	MaxLuminance, MinLuminance int

	// Content light level, in cd/m². Zero when the source did not carry it.
	MaxCLL, MaxFALL int
}

// HasMasteringDisplay reports whether mastering display primaries were found.
func (h *HDR) HasMasteringDisplay() bool {
	return h != nil && h.MaxLuminance > 0
}

// MasterDisplay renders the x265 --master-display argument.
//
// The order is fixed: green, blue, red, white point, luminance.
func (h *HDR) MasterDisplay() string {
	if !h.HasMasteringDisplay() {
		return ""
	}
	return fmt.Sprintf("G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)",
		h.GreenX, h.GreenY,
		h.BlueX, h.BlueY,
		h.RedX, h.RedY,
		h.WhiteX, h.WhiteY,
		h.MaxLuminance, h.MinLuminance,
	)
}

// MaxCLLArg renders the x265 --max-cll argument, or "" when absent.
func (h *HDR) MaxCLLArg() string {
	if h == nil || (h.MaxCLL == 0 && h.MaxFALL == 0) {
		return ""
	}
	return fmt.Sprintf("%d,%d", h.MaxCLL, h.MaxFALL)
}

// sideData is the shape ffprobe uses for both metadata blocks.
type sideData struct {
	Type string `json:"side_data_type"`

	// Mastering display, as rationals like "13250/50000".
	RedX         string `json:"red_x,omitempty"`
	RedY         string `json:"red_y,omitempty"`
	GreenX       string `json:"green_x,omitempty"`
	GreenY       string `json:"green_y,omitempty"`
	BlueX        string `json:"blue_x,omitempty"`
	BlueY        string `json:"blue_y,omitempty"`
	WhiteX       string `json:"white_point_x,omitempty"`
	WhiteY       string `json:"white_point_y,omitempty"`
	MinLuminance string `json:"min_luminance,omitempty"`
	MaxLuminance string `json:"max_luminance,omitempty"`

	// Content light level, as plain integers.
	MaxContent int `json:"max_content,omitempty"`
	MaxAverage int `json:"max_average,omitempty"`
}

// Units x265 expects, which differ from the units ffprobe reports.
const (
	chromaScale    = 50000 // display primaries: 0.00002 units
	luminanceScale = 10000 // luminance: 0.0001 cd/m²
)

// parseHDR extracts HDR10 metadata from ffprobe's side data list.
//
// Returns nil when the stream carries none, which is the ordinary case for SDR
// sources and is not an error.
func parseHDR(list []json.RawMessage) *HDR {
	var hdr HDR
	var found bool

	for _, raw := range list {
		var sd sideData
		if err := json.Unmarshal(raw, &sd); err != nil {
			continue
		}

		switch {
		case strings.Contains(strings.ToLower(sd.Type), "mastering display"):
			hdr.GreenX = scaled(sd.GreenX, chromaScale)
			hdr.GreenY = scaled(sd.GreenY, chromaScale)
			hdr.BlueX = scaled(sd.BlueX, chromaScale)
			hdr.BlueY = scaled(sd.BlueY, chromaScale)
			hdr.RedX = scaled(sd.RedX, chromaScale)
			hdr.RedY = scaled(sd.RedY, chromaScale)
			hdr.WhiteX = scaled(sd.WhiteX, chromaScale)
			hdr.WhiteY = scaled(sd.WhiteY, chromaScale)
			hdr.MaxLuminance = scaled(sd.MaxLuminance, luminanceScale)
			hdr.MinLuminance = scaled(sd.MinLuminance, luminanceScale)
			found = true

		case strings.Contains(strings.ToLower(sd.Type), "content light level"):
			hdr.MaxCLL = sd.MaxContent
			hdr.MaxFALL = sd.MaxAverage
			found = true
		}
	}

	if !found {
		return nil
	}
	return &hdr
}

// scaled converts one of ffprobe's rationals into x265's integer units.
//
// ffprobe reports "13250/50000"; x265 wants the value in units of 0.00002,
// which is the same number when the denominator happens to be 50000 — but the
// denominator is not guaranteed, so the division is done properly.
func scaled(rational string, scale int) int {
	v, ok := parseRational(rational)
	if !ok {
		return 0
	}
	return int(math.Round(v * float64(scale)))
}

// parseRational reads "num/den", or a plain number.
func parseRational(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	num, den, ok := strings.Cut(s, "/")
	if !ok {
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil
	}

	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0, false
	}
	return n / d, true
}

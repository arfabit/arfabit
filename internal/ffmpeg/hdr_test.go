package ffmpeg

import (
	"encoding/json"
	"testing"
)

// Real ffprobe side data from an HDR10 Blu-ray, as rationals.
const hdrSideData = `[
  {"side_data_type":"Mastering display metadata",
   "red_x":"34000/50000","red_y":"16000/50000",
   "green_x":"13250/50000","green_y":"34500/50000",
   "blue_x":"7500/50000","blue_y":"3000/50000",
   "white_point_x":"15635/50000","white_point_y":"16450/50000",
   "min_luminance":"50/10000","max_luminance":"10000000/10000"},
  {"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}
]`

func rawList(t *testing.T, s string) []json.RawMessage {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestParseHDR(t *testing.T) {
	hdr := parseHDR(rawList(t, hdrSideData))
	if hdr == nil {
		t.Fatal("parseHDR returned nil for HDR10 side data")
	}

	// ffprobe reports rationals; x265 wants 0.00002 units for the primaries
	// and 0.0001 cd/m² for luminance.
	if hdr.GreenX != 13250 || hdr.GreenY != 34500 {
		t.Errorf("green = (%d,%d), want (13250,34500)", hdr.GreenX, hdr.GreenY)
	}
	if hdr.MaxLuminance != 10000000 || hdr.MinLuminance != 50 {
		t.Errorf("luminance = (%d,%d), want (10000000,50)", hdr.MaxLuminance, hdr.MinLuminance)
	}
	if hdr.MaxCLL != 1000 || hdr.MaxFALL != 400 {
		t.Errorf("CLL = (%d,%d), want (1000,400)", hdr.MaxCLL, hdr.MaxFALL)
	}

	want := "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	if got := hdr.MasterDisplay(); got != want {
		t.Errorf("MasterDisplay() = %q\nwant %q", got, want)
	}
}

// An SDR stream carries no such side data, which is ordinary, not an error.
func TestParseHDRAbsent(t *testing.T) {
	if hdr := parseHDR(nil); hdr != nil {
		t.Errorf("parseHDR(nil) = %+v, want nil", hdr)
	}
}

// A nil HDR must render empty arguments rather than panic: every SDR encode
// goes through this path.
func TestHDRNilIsSafe(t *testing.T) {
	var hdr *HDR
	if got := hdr.MasterDisplay(); got != "" {
		t.Errorf("MasterDisplay() on nil = %q, want empty", got)
	}
	if got := hdr.MaxCLLArg(); got != "" {
		t.Errorf("MaxCLLArg() on nil = %q, want empty", got)
	}
	if hdr.HasMasteringDisplay() {
		t.Error("HasMasteringDisplay() on nil = true")
	}
}

// Content light level can arrive without mastering display. The partial data
// must still be usable rather than discarded.
func TestParseHDRContentLightOnly(t *testing.T) {
	hdr := parseHDR(rawList(t, `[{"side_data_type":"Content light level metadata","max_content":600,"max_average":120}]`))
	if hdr == nil {
		t.Fatal("parseHDR returned nil despite content light level data")
	}
	if hdr.HasMasteringDisplay() {
		t.Error("HasMasteringDisplay() = true without mastering display data")
	}
	if got := hdr.MaxCLLArg(); got != "600,120" {
		t.Errorf("MaxCLLArg() = %q, want \"600,120\"", got)
	}
}

func TestColorInfoIsHDR(t *testing.T) {
	if !(ColorInfo{Transfer: "smpte2084"}).IsHDR() {
		t.Error("PQ transfer not recognised as HDR")
	}
	if !(ColorInfo{Transfer: "arib-std-b67"}).IsHDR() {
		t.Error("HLG transfer not recognised as HDR")
	}
	if (ColorInfo{Transfer: "bt709"}).IsHDR() {
		t.Error("bt709 reported as HDR")
	}
}

func TestParseRational(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"13250/50000", 0.265, true},
		{"1000", 1000, true},
		{"", 0, false},
		{"5/0", 0, false},
		{"nonsense", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseRational(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseRational(%q) = (%v,%v), want (%v,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

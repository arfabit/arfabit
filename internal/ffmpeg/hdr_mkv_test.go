package ffmpeg

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// An HDR film re-encoded into Matroska must still carry its mastering display
// and light levels, or it plays back grey with no error (§9). They travel in
// the picture itself, where the television reads them, so that is where this
// looks: at what a real encode wrote, not at the arguments that asked for it.
//
// The source is a test pattern, since what is being checked is what ARFABIT
// asks x265 for and what lands in the file. The HDR values are the ones Probe
// hands over for a real disc (a UHD Blu-ray's usual P3 mastering display).
func TestHDRSurvivesIntoMatroska(t *testing.T) {
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "source.mkv")
	if out, err := exec.Command(ffmpegBin, "-v", "error", "-f", "lavfi",
		"-i", "testsrc2=size=640x360:rate=24:duration=1", "-pix_fmt", "yuv420p10le",
		"-c:v", "ffv1", source).CombinedOutput(); err != nil {
		t.Fatalf("could not make a source: %v\n%s", err, out)
	}

	hdr := &HDR{
		GreenX: 13250, GreenY: 34500, BlueX: 7500, BlueY: 3000,
		RedX: 34000, RedY: 16000, WhiteX: 15635, WhiteY: 16450,
		MaxLuminance: 10000000, MinLuminance: 50,
		MaxCLL: 1000, MaxFALL: 400,
	}
	output := filepath.Join(dir, "film.mkv")
	args, err := EncodeRequest{
		Input:  source,
		Output: output,
		Video:  VideoPlan{CRF: 28, Preset: PresetSuperfast},
		HDR:    hdr,
		Color:  ColorInfo{Primaries: "bt2020", Transfer: "smpte2084", Space: "bt2020nc"},
	}.Args()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpegBin, args...).CombinedOutput(); err != nil {
		t.Fatalf("the encode did not finish: %v\n%s", err, out)
	}

	// The first frame's side data is what the decoder found in the picture.
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-read_intervals", "%+#1", "-show_frames", "-show_streams", "-of", "json", output).Output()
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Frames []struct {
			SideData []map[string]any `json:"side_data_list"`
		} `json:"frames"`
		Streams []struct {
			Codec     string `json:"codec_name"`
			Primaries string `json:"color_primaries"`
			Transfer  string `json:"color_transfer"`
			Space     string `json:"color_space"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatal(err)
	}

	if len(probe.Streams) != 1 || probe.Streams[0].Codec != "hevc" {
		t.Fatalf("streams = %+v, want one HEVC stream", probe.Streams)
	}
	s := probe.Streams[0]
	if s.Primaries != "bt2020" || s.Transfer != "smpte2084" || s.Space != "bt2020nc" {
		t.Errorf("colour = %s/%s/%s, want bt2020/smpte2084/bt2020nc", s.Primaries, s.Transfer, s.Space)
	}

	if len(probe.Frames) == 0 {
		t.Fatal("no frame was read")
	}
	var mastering, light map[string]any
	for _, sd := range probe.Frames[0].SideData {
		switch sd["side_data_type"] {
		case "Mastering display metadata":
			mastering = sd
		case "Content light level metadata":
			light = sd
		}
	}

	if mastering == nil {
		t.Fatal("the picture carries no mastering display: it would play back grey")
	}
	for key, want := range map[string]string{
		"max_luminance": "10000000/10000",
		"min_luminance": "50/10000",
		"red_x":         "34000/50000",
		"white_point_y": "16450/50000",
	} {
		if got := mastering[key]; got != want {
			t.Errorf("mastering display %s = %v, want %s", key, got, want)
		}
	}

	if light == nil {
		t.Fatal("the picture carries no content light levels")
	}
	if light["max_content"] != float64(1000) || light["max_average"] != float64(400) {
		t.Errorf("light levels = %v/%v, want 1000/400", light["max_content"], light["max_average"])
	}
}

package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/store"
)

// A package that could not be made is refused before it joins the line.
func TestPackageIsCheckedFirst(t *testing.T) {
	video := store.Item{Kind: store.KindVideo, Action: store.ActionCopy}
	for _, tc := range []struct {
		name string
		pkg  store.Project
		want string
	}{
		{"no picture", store.Project{Items: []store.Item{{Kind: store.KindAudio, Action: store.ActionCopy}}}, "needs a picture"},
		{"two pictures", store.Project{Items: []store.Item{video, video}}, "one picture"},
		{"a container ARFABIT does not make", store.Project{Containers: []string{"avi"}, Items: []store.Item{video}}, "AVI"},
		{"picture subtitles in MP4", store.Project{Containers: []string{"mp4"}, Items: []store.Item{video,
			{Kind: store.KindSubtitle, Action: store.ActionCopy, Codec: "hdmv_pgs_subtitle"}}}, "picture subtitles"},
		{"TrueHD in MP4", store.Project{Containers: []string{"mp4"}, Items: []store.Item{video,
			{Kind: store.KindAudio, Action: store.ActionCopy, Codec: "truehd", Channels: 8}}}, "experimental"},
		{"E-AC-3 7.1", store.Project{Items: []store.Item{video,
			{Kind: store.KindAudio, Action: store.ActionConvert, To: "eac3", Channels: 8}}}, "six channels"},
		{"E-AC-3 stereo", store.Project{Items: []store.Item{video,
			{Kind: store.KindAudio, Action: store.ActionConvert, To: "eac3", Channels: 2}}}, "AAC for stereo"},
		{"E-AC-3 downmixed to stereo", store.Project{Items: []store.Item{video,
			{Kind: store.KindAudio, Action: store.ActionConvert, To: "eac3", Channels: 6, OutChannels: 2}}}, "AAC for stereo"},
		{"more than the track has", store.Project{Items: []store.Item{video,
			{Kind: store.KindAudio, Action: store.ActionConvert, To: "aac", Channels: 2, Bitrate: "768k", SourceBitrate: 192000}}}, "192 kbps"},
		{"subtitles converted where they cannot be read", store.Project{Items: []store.Item{video,
			{Kind: store.KindSubtitle, Action: store.ActionConvert, To: "srt", Codec: "hdmv_pgs_subtitle"}}}, "cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkProject(&tc.pkg, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	ok := store.Project{Items: []store.Item{video}}
	if err := checkProject(&ok, false); err != nil {
		t.Errorf("a picture copied as it is was refused: %v", err)
	}
	if len(ok.Containers) != 1 || ok.Containers[0] != "mkv" {
		t.Errorf("containers = %v, want MKV when none is chosen", ok.Containers)
	}
}

// A stretch of an original becomes a clip made exactly from its line items, in
// their order, and the piece cut to make it does not stay behind.
func TestPackageMakesAClipFromItsLineItems(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:03,500\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "original.mkv")
	out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=4:sample_rate=48000",
		"-i", srt,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3",
		"-c:v", "libx264", "-c:a:0", "ac3", "-ac:a:0", "6", "-c:a:1", "pcm_s24le", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=eng", "-metadata:s:s:0", "language=eng",
		original).CombinedOutput()
	if err != nil {
		t.Fatalf("could not make an original: %v\n%s", err, out)
	}

	r := runnerWithFolders(t)
	r.Slots = NewSlots(1)

	job, err := r.StartProject(context.Background(), ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{
			Edition: "Trial",
			At:      time.Second, Length: 2 * time.Second,
			Items: []store.Item{
				{Kind: store.KindVideo, Source: 0, Action: store.ActionConvert, To: "hevc", CRF: 30, Preset: "superfast"},
				// Stereo converted first, so order is seen to be kept.
				{Kind: store.KindAudio, Source: 2, Action: store.ActionConvert, To: "flac", Channels: 2, Lang: "eng", Lossless: true},
				{Kind: store.KindAudio, Source: 1, Action: store.ActionCopy, Codec: "ac3", Channels: 6, Lang: "eng"},
				{Kind: store.KindSubtitle, Source: 3, Action: store.ActionCopy, Lang: "eng"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for len(r.Active()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if job.State != store.StateDone {
		t.Fatalf("the package ended as %s: %s\n%s", job.State, job.Note, job.Detail)
	}

	folder := filepath.Join(r.Config.Paths.Clips, "Test Film")
	entries, _ := os.ReadDir(folder)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the lab folder holds %v, want one clip and nothing else", names)
	}
	clip := filepath.Join(folder, entries[0].Name())
	if !strings.Contains(entries[0].Name(), "{edition-Lab 001 - Trial") || filepath.Ext(clip) != ".mkv" {
		t.Errorf("clip is called %q", entries[0].Name())
	}

	info, err := ffmpeg.Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range info.Streams {
		got = append(got, s.Codec)
	}
	if strings.Join(got, ",") != "hevc,flac,ac3,subrip" {
		t.Errorf("streams = %v, want hevc,flac,ac3,subrip", got)
	}
	// Copied streams can only start on a keyframe, so a clip starts at the
	// last one before the time asked for (here the original's only one, at the
	// start). What matters is that it covers the stretch, and that picture
	// and sound run together.
	video, sound := streamSeconds(t, clip, "v:0"), streamSeconds(t, clip, "a:1")
	if video < 2 || video > 3.2 {
		t.Errorf("the picture lasts %.2fs, want the 2s stretch and at most its lead-in", video)
	}
	if d := video - sound; d > 0.1 || d < -0.1 {
		t.Errorf("picture %.2fs and copied sound %.2fs do not run together", video, sound)
	}
	if job.Comparison.Clips == nil {
		t.Error("the clip does not say what the whole film would come to")
	}
}

// streamSeconds is how long one stream of a file lasts, as Matroska records it.
func streamSeconds(t *testing.T, path, stream string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", stream,
		"-show_entries", "stream_tags=DURATION", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var h, m int
	var sec float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d:%d:%f", &h, &m, &sec); err != nil {
		t.Fatalf("could not read the duration of %s: %q", stream, out)
	}
	return float64(h*3600+m*60) + sec
}

// A whole film goes into the library under its edition, and onto the
// library's list.
func TestPackageMakesAFilmForTheLibrary(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}

	dir := t.TempDir()
	original := filepath.Join(dir, "original.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "ac3", original).CombinedOutput(); err != nil {
		t.Fatalf("could not make an original: %v\n%s", err, out)
	}

	r := runnerWithFolders(t)
	job, err := r.StartProject(context.Background(), ProjectRequest{
		Original: original, Film: "Test Film", Year: 2026,
		ClipsDir: r.Config.Paths.Clips, LibraryDir: r.Config.Paths.Library,
		Project: store.Project{Edition: "Archive", Items: []store.Item{
			{Kind: store.KindVideo, Source: 0, Action: store.ActionCopy},
			{Kind: store.KindAudio, Source: 1, Action: store.ActionCopy},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(r.Active()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if job.State != store.StateDone {
		t.Fatalf("the package ended as %s: %s\n%s", job.State, job.Note, job.Detail)
	}

	want := filepath.Join(r.Config.Paths.Library, "Test Film (2026)", "Test Film (2026) {edition-Archive}.mkv")
	if job.Delivery != want {
		t.Errorf("Delivery = %q, want %q", job.Delivery, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the film is not in the library: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(r.Store.Root, "library", "index.jsonl"))
	if err != nil || !strings.Contains(string(index), "{edition-Archive}.mkv") {
		t.Errorf("the film is not on the library's list: %v %s", err, index)
	}
}

// A package planned from a disc's scan is pointed at the original's own tracks,
// which MakeMKV numbers its own way, by what each track is. Uncompressed disc
// sound, which MakeMKV turns into FLAC, is still found, and a track kept and
// converted stays one track.
func TestPackageIsBoundToTheMaster(t *testing.T) {
	pkg := &store.Project{Items: []store.Item{
		{Kind: store.KindVideo, Source: 0},
		{Kind: store.KindAudio, Source: 3, Lang: "eng", Codec: "pcm", Channels: 6},
		{Kind: store.KindAudio, Source: 3, Lang: "eng", Codec: "pcm", Channels: 6, Action: store.ActionConvert},
		{Kind: store.KindAudio, Source: 5, Lang: "fra", Codec: "ac3", Channels: 6},
		{Kind: store.KindSubtitle, Source: 9, Lang: "fra", Codec: "hdmv_pgs_subtitle"},
	}}
	original := []Track{
		{Index: 0, Kind: store.KindVideo, Codec: "h264"},
		{Index: 1, Kind: store.KindAudio, Lang: "eng", Codec: "flac", Channels: 6},
		{Index: 2, Kind: store.KindAudio, Lang: "fra", Codec: "ac3", Channels: 6},
		{Index: 3, Kind: store.KindSubtitle, Lang: "eng", Codec: "hdmv_pgs_subtitle"},
		{Index: 4, Kind: store.KindSubtitle, Lang: "fra", Codec: "hdmv_pgs_subtitle"},
	}

	if err := bindToOriginal(pkg, nil, original); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, it := range pkg.Items {
		got = append(got, it.Source)
	}
	if fmt.Sprint(got) != "[0 1 1 2 4]" {
		t.Errorf("bound to %v, want [0 1 1 2 4]", got)
	}
	if pkg.Items[1].Codec != "flac" {
		t.Errorf("the line still says %q; the original holds FLAC", pkg.Items[1].Codec)
	}

	missing := &store.Project{Items: []store.Item{{Kind: store.KindAudio, Lang: "jpn", Channels: 2, Label: "Japanese · Stereo"}}}
	if err := bindToOriginal(missing, nil, original); err == nil || !strings.Contains(err.Error(), "japanese") {
		t.Errorf("a track the original lacks was not reported: %v", err)
	}
}

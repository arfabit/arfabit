// Package lab renders short clips under different settings, so that choosing
// a quality is a matter of watching rather than guessing.
//
// The clips are made from a Master, which already exists, so trying five
// settings costs minutes rather than the hours a full encode takes.
package lab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/meta"
)

// Clip is one setting rendered from the master.
type Clip struct {
	// Name identifies the setting, and names the file.
	Name string `json:"name"`

	// Video and Audio say what was asked for. Either may be empty, so that a
	// picture setting can be judged without the sound changing under it, and
	// the other way round.
	Video VideoSetting `json:"video"`
	Audio AudioSetting `json:"audio"`

	// Path is the rendered clip, for watching.
	Path string `json:"path"`

	// Size is what it came to.
	Size int64 `json:"size"`

	// Took is how long it took to make, which extrapolates to the whole film.
	Took time.Duration `json:"took"`

	// Problem is whatever went wrong, kept whole.
	Problem string `json:"problem,omitempty"`
}

// VideoSetting is one way of treating the picture.
type VideoSetting struct {
	// Copy leaves the picture exactly as it is, which is the honest baseline
	// every other setting is judged against.
	Copy bool `json:"copy"`

	CRF    int    `json:"crf,omitempty"`
	Preset string `json:"preset,omitempty"`
}

// AudioSetting is one way of treating the sound.
type AudioSetting struct {
	// Copy leaves the chosen track untouched.
	Copy bool `json:"copy"`

	Codec   string `json:"codec,omitempty"`
	Bitrate string `json:"bitrate,omitempty"`

	// Channels of 0 keeps the source layout.
	Channels int `json:"channels,omitempty"`

	// SourceIndex is which track of the master to use.
	SourceIndex int `json:"source_index"`
}

// Request is one run of the lab.
type Request struct {
	// Master is the file to take the clip from.
	Master string

	// At is where in the film to take it from.
	At time.Duration

	// Length is how much to take. A minute is usually enough to see
	// compression artefacts; ten is enough to hear them.
	Length time.Duration

	// Settings are what to try.
	Settings []Clip

	// Film is the movie's name, which becomes both the folder and the stem of
	// every clip in it, so a media manager recognises them all as the same
	// film.
	Film string

	// OutputDir is the lab folder. Clips go into a folder per film inside it.
	OutputDir string

	// Run numbers this batch. Clips from one run share a number, which is how
	// a comparison stays recognisable weeks later.
	Run int

	// WholeFilm says this is a film rather than a test clip, which changes
	// both where it goes and what it is called.
	WholeFilm bool

	// FilmTitle names the film when the whole of it is being made, so the
	// result is named the way the library names everything else.
	FilmTitle interface {
		VideoName(edition string) string
	}

	// OnClip is called as each clip finishes, so the page fills in rather
	// than waiting for the whole set.
	OnClip func(Clip)
}

// Durations the lab offers.
//
// Short clips show compression artefacts; longer ones are needed to judge
// sound, where a single line of dialogue tells you little.
var Durations = []time.Duration{
	5 * time.Second,
	10 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	10 * time.Minute,
}

// Run renders every setting and reports what each cost.
//
// A setting that fails does not stop the others: the point of the lab is
// comparison, and four results out of five is still a comparison.
func Run(ctx context.Context, req Request) ([]Clip, error) {
	if req.Master == "" {
		return nil, fmt.Errorf("the lab needs a master file to take a clip from")
	}

	if req.Film == "" {
		req.Film = strings.TrimSuffix(filepath.Base(req.Master), filepath.Ext(req.Master))
	}

	// A whole film goes where films go, under the name films have. A clip
	// goes into a folder per film in the lab, as elsewhere, so a media
	// manager pointed at it sees films rather than a heap of clips.
	filmDir := req.OutputDir
	if !req.WholeFilm {
		filmDir = filepath.Join(req.OutputDir, req.Film)
	}
	if err := os.MkdirAll(filmDir, 0o755); err != nil {
		return nil, err
	}

	if req.Run == 0 && !req.WholeFilm {
		req.Run = NextRun(filmDir)
	}

	info, err := ffmpeg.Probe(ctx, req.Master)
	if err != nil {
		return nil, fmt.Errorf("the master file could not be read: %w", err)
	}

	video := info.VideoStream()
	if video == nil {
		return nil, fmt.Errorf("the master file has no picture")
	}

	results := make([]Clip, 0, len(req.Settings))
	for _, setting := range req.Settings {
		clip := render(ctx, req, setting, video)
		results = append(results, clip)

		if req.OnClip != nil {
			req.OnClip(clip)
		}
	}

	return results, nil
}

// render makes one clip.
func render(ctx context.Context, req Request, setting Clip, video *ffmpeg.Stream) Clip {
	setting.Path = outputPath(req, setting.Name)

	args := clipArgs(req, setting, video)

	start := time.Now()
	err := ffmpeg.Run(ctx, args, ffmpeg.RunOptions{Duration: req.Length})
	setting.Took = time.Since(start)

	if err != nil {
		var runErr *ffmpeg.RunError
		if ok := asRunError(err, &runErr); ok {
			setting.Problem = runErr.Output
		} else {
			setting.Problem = err.Error()
		}
		return setting
	}

	if info, err := os.Stat(setting.Path); err == nil {
		setting.Size = info.Size()
	}
	return setting
}

// clipArgs builds the ffmpeg command for one clip.
//
// The seek comes before the input, which makes ffmpeg jump straight to the
// right place instead of decoding everything up to it — the difference
// between a clip taking seconds and taking as long as the film.
func clipArgs(req Request, setting Clip, video *ffmpeg.Stream) []string {
	args := []string{"-hide_banner", "-y"}

	// Seeking before the input makes ffmpeg jump straight to the right place
	// instead of decoding everything up to it.
	if req.At > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", req.At.Seconds()))
	}
	args = append(args, "-i", req.Master)

	// No length means run to the end. A length longer than what remains does
	// the same thing, which is why nothing here has to check.
	if req.Length > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", req.Length.Seconds()))
	}

	args = append(args,
		"-map", fmt.Sprintf("0:%d", video.Index),
		"-map", fmt.Sprintf("0:%d", setting.Audio.SourceIndex),
	)

	if setting.Video.Copy {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", "libx265",
			"-crf", fmt.Sprint(setting.Video.CRF),
			"-preset", setting.Video.Preset,
			"-profile:v", "main10",
			"-pix_fmt", "yuv420p10le",
		)
		if params := hdrParams(video); params != "" {
			args = append(args, "-x265-params", params)
		}
		args = append(args, video.ColorInfo.Args()...)
	}

	if setting.Audio.Copy {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", setting.Audio.Codec)
		if f := ffmpeg.LosslessFrames(setting.Audio.Codec); f != "" {
			args = append(args, "-af", f)
		}
		if setting.Audio.Bitrate != "" {
			args = append(args, "-b:a", setting.Audio.Bitrate)
		}
		if setting.Audio.Channels > 0 {
			args = append(args, "-ac", fmt.Sprint(setting.Audio.Channels))
		}
	}

	return append(args, setting.Path)
}

// hdrParams carries the source's HDR metadata into a clip.
//
// A lab clip that lost it would look grey next to one that kept it, and the
// comparison would be of the wrong thing entirely (§9).
func hdrParams(video *ffmpeg.Stream) string {
	if !video.ColorInfo.IsHDR() {
		return ""
	}

	params := []string{"hdr10=1", "hdr10-opt=1", "repeat-headers=1"}
	if md := video.HDR.MasterDisplay(); md != "" {
		params = append(params, "master-display="+md)
	}
	if cll := video.HDR.MaxCLLArg(); cll != "" {
		params = append(params, "max-cll="+cll)
	}
	return strings.Join(params, ":")
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

	// WholeFilm is the size the whole film would come to.
	WholeFilm int64 `json:"whole_film"`

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
			WholeFilm:   int64(float64(clip.Size) * scale),
			EncodeTime:  time.Duration(float64(clip.Took) * scale),
		}

		if smallestSize == 0 || row.WholeFilm < smallestSize {
			smallestSize = row.WholeFilm
		}
		if quickest == 0 || row.EncodeTime < quickest {
			quickest = row.EncodeTime
		}
		rows = append(rows, row)
	}

	for i := range rows {
		if rows[i].WholeFilm > 0 && smallestSize > 0 {
			rows[i].SizeShare = float64(smallestSize) / float64(rows[i].WholeFilm) * 100
		}
		if rows[i].EncodeTime > 0 && quickest > 0 {
			rows[i].TimeShare = float64(quickest) / float64(rows[i].EncodeTime) * 100
		}
	}

	sort.SliceStable(rows, func(a, b int) bool {
		return rows[a].WholeFilm < rows[b].WholeFilm
	})

	c.Clips = rows
	return c
}

// outputPath decides where one result goes and what it is called.
func outputPath(req Request, setting string) string {
	if req.WholeFilm && req.FilmTitle != nil {
		// A film, named the way the library names every film, with the
		// blueprint as the edition so several versions sit side by side.
		return filepath.Join(req.OutputDir, req.FilmTitle.VideoName(setting))
	}
	return filepath.Join(req.OutputDir, req.Film, ClipName(req.Film, req.Run, setting, req.At))
}

// ClipName names a clip the way the library names a film, with the setting as
// the edition.
//
// The shape is deliberate: a media manager pointed at the lab folder sees one
// film with several editions and will play them one after another, which is
// exactly what comparing them means. The run number keeps one afternoon's
// comparison together and separate from the next.
//
//	Crime 101 (2025) {edition-Lab 001 - crf20-medium - 1h15m20s}.mkv
func ClipName(film string, run int, setting string, at time.Duration) string {
	if film == "" {
		film = "Clip"
	}

	return fmt.Sprintf("%s {edition-%s}"+meta.VideoExt,
		trimName(film, 60),
		editionTag(run, setting, at),
	)
}

// editionTag is what a media manager shows as the edition name.
//
// Braces would close the tag early and are removed; everything else is left
// readable, because this is the label somebody picks from on a television.
func editionTag(run int, setting string, at time.Duration) string {
	tag := fmt.Sprintf("Lab %03d - %s - %s", run, setting, timestamp(at))
	return strings.NewReplacer("{", "", "}", "").Replace(tag)
}

// NextRun works out which run this is, by looking at what is already there.
//
// The folder is the record, so a run number survives restarts and picks up
// where it left off rather than starting again at one.
func NextRun(filmDir string) int {
	entries, err := os.ReadDir(filmDir)
	if err != nil {
		return 1
	}

	highest := 0
	for _, e := range entries {
		var run int
		if _, err := fmt.Sscanf(runNumberIn(e.Name()), "%d", &run); err != nil {
			continue
		}
		if run > highest {
			highest = run
		}
	}
	return highest + 1
}

// runNumberIn pulls "001" out of "... {edition-Lab 001 - ...}.mkv".
func runNumberIn(name string) string {
	const marker = "{edition-Lab "
	start := strings.Index(name, marker)
	if start < 0 {
		return ""
	}

	rest := name[start+len(marker):]
	if space := strings.IndexAny(rest, " -}"); space >= 0 {
		rest = rest[:space]
	}
	return rest
}

// timestamp renders a position as something that sorts and reads properly.
func timestamp(d time.Duration) string {
	return fmt.Sprintf("%dh%02dm%02ds",
		int(d/time.Hour),
		int(d/time.Minute)%60,
		int(d/time.Second)%60,
	)
}

func trimName(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return strings.TrimSpace(s[:limit])
}

// safeName turns a setting name into a filename.
func safeName(name string) string {
	if name == "" {
		return "clip"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, name)
}

// asRunError unwraps an ffmpeg failure, keeping its output.
func asRunError(err error, target **ffmpeg.RunError) bool {
	for err != nil {
		if runErr, ok := err.(*ffmpeg.RunError); ok {
			*target = runErr
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

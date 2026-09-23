package ffmpeg

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Preset is an x265 speed preset. Slower presets produce smaller files at the
// same quality.
type Preset string

const (
	PresetSuperfast Preset = "superfast"
	PresetMedium    Preset = "medium"
	PresetSlow      Preset = "slow"
	PresetSlower    Preset = "slower"
	PresetVeryslow  Preset = "veryslow"
)

// Presets are the speeds ARFABIT offers, fastest first.
var Presets = []Preset{PresetSuperfast, PresetMedium, PresetSlow, PresetSlower, PresetVeryslow}

// Valid reports whether p is one of the offered presets.
func (p Preset) Valid() bool {
	for _, known := range Presets {
		if p == known {
			return true
		}
	}
	return false
}

// VideoPlan says what to do with the picture.
type VideoPlan struct {
	// Copy passes the video through untouched. Only available when the source
	// codec is one Apple TV decodes natively, which in practice means HEVC on
	// a UHD disc.
	Copy bool

	CRF    int
	Preset Preset
}

// AudioTrack is one output audio track.
type AudioTrack struct {
	// SourceIndex is the stream index in the input file.
	SourceIndex int

	// Copy passes the track through untouched, which is possible when the
	// source is already AC-3, E-AC-3 or AAC (§9).
	Copy bool

	Codec    string // "aac", "eac3" — ignored when Copy
	Bitrate  string // "256k"
	Channels int    // 2 for a stereo downmix, 0 to keep the source layout
	Title    string
	Lang     string
	Default  bool
}

// SubtitleTrack is one output subtitle track, converted to mov_text.
type SubtitleTrack struct {
	// Path to an SRT file on disk. MP4 cannot carry the bitmap subtitles a
	// disc ships, so subtitles reach here only after OCR.
	Path    string
	Lang    string
	Title   string
	Forced  bool
	Default bool
}

// EncodeRequest is one packaging job: an input file plus what to make of it.
type EncodeRequest struct {
	Input     string
	Output    string
	Video     VideoPlan
	Audio     []AudioTrack
	Subtitles []SubtitleTrack

	// VideoSourceIndex is the input stream index of the picture.
	VideoSourceIndex int

	// HDR is the source's metadata, propagated when the video is re-encoded.
	// Nil for SDR sources.
	HDR *HDR

	// Color describes the source's colour space, propagated alongside HDR.
	Color ColorInfo

	// Chapters copies chapter markers into the output.
	Chapters bool
}

// nativeAudioCodecs are the codecs Apple TV decodes, and so the ones worth
// copying rather than re-encoding.
var nativeAudioCodecs = map[string]bool{
	"ac3":  true,
	"eac3": true,
	"aac":  true,
}

// CanCopyAudio reports whether a source audio codec can be passed through.
func CanCopyAudio(codec string) bool {
	return nativeAudioCodecs[strings.ToLower(codec)]
}

// nativeVideoCodecs are the codecs Apple TV decodes. MPEG-2 and VC-1 are
// absent deliberately: DVDs and some older Blu-rays must be re-encoded.
var nativeVideoCodecs = map[string]bool{
	"h264": true,
	"hevc": true,
}

// CanCopyVideo reports whether a source video codec can be passed through.
func CanCopyVideo(codec string) bool {
	return nativeVideoCodecs[strings.ToLower(codec)]
}

// Args builds the ffmpeg command line.
//
// Ordering matters in two places: inputs come before the maps that reference
// them, and output tracks are emitted multichannel-first because Apple TV
// selects the first compatible audio track.
func (r EncodeRequest) Args() ([]string, error) {
	if r.Input == "" || r.Output == "" {
		return nil, fmt.Errorf("encode: input and output are both required")
	}
	if !r.Video.Copy && !r.Video.Preset.Valid() {
		return nil, fmt.Errorf("encode: %q is not an offered preset", r.Video.Preset)
	}

	args := []string{"-hide_banner", "-y", "-i", r.Input}

	// Each SRT is a separate input, numbered from 1.
	for _, s := range r.Subtitles {
		args = append(args, "-i", s.Path)
	}

	args = append(args, "-map", fmt.Sprintf("0:%d", r.VideoSourceIndex))
	for _, a := range r.Audio {
		args = append(args, "-map", fmt.Sprintf("0:%d", a.SourceIndex))
	}
	for i := range r.Subtitles {
		args = append(args, "-map", fmt.Sprintf("%d:0", i+1))
	}

	args = append(args, r.videoArgs()...)
	args = append(args, r.audioArgs()...)
	args = append(args, r.subtitleArgs()...)

	if r.Chapters {
		args = append(args, "-map_chapters", "0")
	} else {
		args = append(args, "-map_chapters", "-1")
	}

	// faststart moves the index to the front so playback can begin before the
	// whole file is available.
	args = append(args, "-movflags", "+faststart", r.Output)

	return args, nil
}

func (r EncodeRequest) videoArgs() []string {
	if r.Video.Copy {
		return []string{"-c:v", "copy"}
	}

	args := []string{
		"-c:v", "libx265",
		"-crf", strconv.Itoa(r.Video.CRF),
		"-preset", string(r.Video.Preset),
		// main10 always, even for SDR: 10-bit internal precision reduces
		// banding in gradients at essentially no size cost, and Apple TV plays
		// HEVC Main10 natively.
		"-profile:v", "main10",
		"-pix_fmt", "yuv420p10le",
		// hvc1 rather than hev1: Apple's players require this tag in MP4.
		"-tag:v", "hvc1",
	}

	if x265 := r.x265Params(); x265 != "" {
		args = append(args, "-x265-params", x265)
	}

	// Colour description is carried on the output stream as well as inside the
	// bitstream, so players that read the container get it too.
	if r.Color.Primaries != "" {
		args = append(args, "-color_primaries", r.Color.Primaries)
	}
	if r.Color.Transfer != "" {
		args = append(args, "-color_trc", r.Color.Transfer)
	}
	if r.Color.Space != "" {
		args = append(args, "-colorspace", r.Color.Space)
	}

	return args
}

// x265Params builds the -x265-params value.
//
// ffmpeg forwards the basic colour tags to libx265 on its own, but mastering
// display and content light level are not forwarded — and those are the two a
// television uses to tone map. Omitting them yields a grey picture and no
// error, so they are passed explicitly. See docs/ARCHITECTURE.md §9.
func (r EncodeRequest) x265Params() string {
	var params []string

	if r.Color.IsHDR() {
		params = append(params, "hdr10=1", "hdr10-opt=1", "repeat-headers=1")
		if r.Color.Primaries != "" {
			params = append(params, "colorprim="+r.Color.Primaries)
		}
		if r.Color.Transfer != "" {
			params = append(params, "transfer="+r.Color.Transfer)
		}
		if r.Color.Space != "" {
			params = append(params, "colormatrix="+r.Color.Space)
		}
	}

	if md := r.HDR.MasterDisplay(); md != "" {
		params = append(params, "master-display="+md)
	}
	if cll := r.HDR.MaxCLLArg(); cll != "" {
		params = append(params, "max-cll="+cll)
	}

	return strings.Join(params, ":")
}

func (r EncodeRequest) audioArgs() []string {
	var args []string

	for i, a := range r.Audio {
		out := strconv.Itoa(i)

		if a.Copy {
			args = append(args, "-c:a:"+out, "copy")
		} else {
			args = append(args, "-c:a:"+out, a.Codec)
			if a.Bitrate != "" {
				args = append(args, "-b:a:"+out, a.Bitrate)
			}
			if a.Channels > 0 {
				args = append(args, "-ac:"+out, strconv.Itoa(a.Channels))
			}
		}

		if a.Lang != "" {
			args = append(args, "-metadata:s:a:"+out, "language="+a.Lang)
		}
		if a.Title != "" {
			args = append(args, "-metadata:s:a:"+out, "title="+a.Title)
		}
		args = append(args, "-disposition:a:"+out, dispositionOf(a.Default, false))
	}

	return args
}

func (r EncodeRequest) subtitleArgs() []string {
	if len(r.Subtitles) == 0 {
		return nil
	}

	// mov_text is the only subtitle format MP4 carries.
	args := []string{"-c:s", "mov_text"}

	for i, s := range r.Subtitles {
		out := strconv.Itoa(i)
		if s.Lang != "" {
			args = append(args, "-metadata:s:s:"+out, "language="+s.Lang)
		}
		if s.Title != "" {
			args = append(args, "-metadata:s:s:"+out, "title="+s.Title)
		}
		args = append(args, "-disposition:s:"+out, dispositionOf(s.Default, s.Forced))
	}

	return args
}

// dispositionOf renders ffmpeg's disposition flags. "0" clears them, which is
// what an unflagged track needs — ffmpeg otherwise inherits the source's.
func dispositionOf(isDefault, isForced bool) string {
	var flags []string
	if isDefault {
		flags = append(flags, "default")
	}
	if isForced {
		flags = append(flags, "forced")
	}
	if len(flags) == 0 {
		return "0"
	}
	sort.Strings(flags)
	return strings.Join(flags, "+")
}

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
	// codec plays directly (§4), which in practice means HEVC on a UHD disc.
	Copy bool

	CRF    int
	Preset Preset
}

// AudioTrack is one output audio track.
type AudioTrack struct {
	// SourceIndex is the stream index in the input file.
	SourceIndex int

	// Copy passes the track through untouched, which is possible when the
	// source already plays directly (§4, §9).
	Copy bool

	Codec    string // "flac", "aac", "eac3" — ignored when Copy
	Bitrate  string // "256k"
	Channels int    // 2 for a stereo downmix, 0 to keep the source layout
	Title    string
	Lang     string
	Default  bool
}

// SubtitleTrack is one output subtitle track, copied from the input as it
// is. Subtitles read into text by OCR are not put in the file; they go beside
// it as SRT files (§10).
type SubtitleTrack struct {
	SourceIndex int

	Lang    string
	Title   string
	Forced  bool
	Default bool
}

// EncodeRequest is one packaging job: an input file plus what to make of it.
type EncodeRequest struct {
	Input  string
	Output string
	Video  VideoPlan
	Audio  []AudioTrack

	// Subtitles are in the order they go into the file.
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

	// MP4 makes an MP4 rather than Matroska. §4 has nothing tested for MP4;
	// what differs here is only what ffmpeg needs to write one at all.
	MP4 bool

	// HEVC says the picture is HEVC, copied or made, which MP4 labels.
	HEVC bool
}

// carriedAudioCodecs are the sound formats Matroska carries as they are,
// which is every one a disc has. Whether a device then plays one directly is
// a separate question, answered by what was tested (§4, package playback),
// and changing a track because of it is the user's choice, never this one's.
// "dts" covers DTS-HD Master Audio, which shares its codec name with DTS.
var carriedAudioCodecs = map[string]bool{
	"ac3":    true,
	"eac3":   true,
	"aac":    true,
	"dts":    true,
	"truehd": true,
	"flac":   true,
	"alac":   true,
	"pcm":    true,
	"mp2":    true,
	"mp3":    true,
	"opus":   true,
}

// CanCopyAudio reports whether a source audio codec can be passed through
// into the output. Uncompressed sound comes in one "pcm_" codec per sample
// format.
func CanCopyAudio(codec string) bool {
	codec = strings.ToLower(codec)
	return carriedAudioCodecs[codec] || strings.HasPrefix(codec, "pcm_")
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

	args = append(args, "-map", fmt.Sprintf("0:%d", r.VideoSourceIndex))
	for _, a := range r.Audio {
		args = append(args, "-map", fmt.Sprintf("0:%d", a.SourceIndex))
	}
	for _, s := range r.Subtitles {
		args = append(args, "-map", fmt.Sprintf("0:%d", s.SourceIndex))
	}

	args = append(args, r.videoArgs()...)
	args = append(args, r.audioArgs()...)
	args = append(args, r.subtitleArgs()...)

	if r.Chapters {
		args = append(args, "-map_chapters", "0")
	} else {
		args = append(args, "-map_chapters", "-1")
	}

	// MakeMKV writes each track's size, duration and bitrate into the original.
	// Carried across, they describe the source rather than what was made — a
	// converted track claiming the bitrate of the one it came from — so they
	// are left behind. Language and title are set on each track explicitly.
	args = append(args, "-map_metadata", "-1")

	// Matroska (§0.2) needs nothing more. For MP4, ffmpeg labels HEVC
	// "hev1" unless asked; "hvc1" is the label under which the picture's
	// parameter sets travel outside the picture, which is what ARFABIT asks
	// for. Which of the two an Apple TV plays is untested (§4).
	if r.MP4 && r.HEVC {
		args = append(args, "-tag:v", "hvc1")
	}
	args = append(args, r.Output)

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
	}

	if x265 := r.x265Params(); x265 != "" {
		args = append(args, "-x265-params", x265)
	}

	return append(args, r.Color.Args()...)
}

// Args carries the colour description into the output's container label as
// well as into the picture, so a player that reads either finds it.
//
// The frames themselves are stamped with it, not only the encoder. Given the
// encoder options alone, ffmpeg's Matroska muxer labelled the file with the
// matrix and nothing else, and a reader that trusts the label then takes an
// HDR film's primaries and transfer to be unknown (§9). Measured with ffmpeg
// 9.0.2; HDRSurvivesIntoMatroska checks it still holds.
func (c ColorInfo) Args() []string {
	var params, opts []string
	add := func(filter, option, value string) {
		if value == "" || value == "unknown" {
			return
		}
		params = append(params, filter+"="+value)
		opts = append(opts, option, value)
	}
	add("color_primaries", "-color_primaries", c.Primaries)
	add("color_trc", "-color_trc", c.Transfer)
	add("colorspace", "-colorspace", c.Space)

	if len(params) == 0 {
		return nil
	}
	return append([]string{"-vf", "setparams=" + strings.Join(params, ":")}, opts...)
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
			if f := LosslessFrames(a.Codec); f != "" {
				args = append(args, "-filter:a:"+out, f)
			}
			if a.Bitrate != "" {
				args = append(args, "-b:a:"+out, a.Bitrate)
			}
			if a.Channels > 0 {
				// "-ac:a:N", not "-ac:N": the bare form counts every stream in
				// the file, so it silently applied to the wrong one and the
				// downmix never happened.
				args = append(args, "-ac:a:"+out, strconv.Itoa(a.Channels))
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

// LosslessFrames returns a filter that hands a lossless encoder frames of an
// even size, or "" for codecs that need none.
//
// TrueHD decodes into frames of uneven sizes, and a cut can start on a tiny
// one. FLAC and ALAC refuse to start on a frame of a few samples, and whether
// a cut lands on one depends on where it starts: a clip from 20:05.000 worked
// and one from 20:05.121 did not. Evening the frames out changes no sample.
func LosslessFrames(codec string) string {
	switch codec {
	case "flac", "alac":
		return "asetnsamples=n=4096:p=0"
	}
	return ""
}

func (r EncodeRequest) subtitleArgs() []string {
	var args []string
	for i, s := range r.Subtitles {
		out := strconv.Itoa(i)
		// A copied track stays as it is, whatever it is; Matroska carries
		// picture subtitles too, though showing them costs a player more
		// (§4). MP4 holds text only in its own format: ffmpeg 9.0.2 refused
		// SRT copied into one, and wrote it as mov_text when asked.
		if r.MP4 {
			args = append(args, "-c:s:"+out, "mov_text")
		} else {
			args = append(args, "-c:s:"+out, "copy")
		}
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

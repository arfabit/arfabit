// Package ffmpeg drives ffmpeg and ffprobe for packaging and encoding.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MediaInfo is what ffprobe reports about a file.
type MediaInfo struct {
	Duration float64
	Streams  []Stream
}

// Stream is one stream in a media file.
type Stream struct {
	Index     int
	Kind      string // "video", "audio", "subtitle"
	Codec     string // "hevc", "ac3", "hdmv_pgs_subtitle"
	Lang      string
	Title     string
	Width     int
	Height    int
	Channels  int
	Layout    string
	BitRate   int
	Default   bool
	Forced    bool
	HDR       *HDR // video only, nil when the stream is not HDR
	ColorInfo ColorInfo
}

// ColorInfo is the basic colour description every video stream carries.
type ColorInfo struct {
	Primaries string // "bt2020"
	Transfer  string // "smpte2084"
	Space     string // "bt2020nc"
}

// IsHDR reports whether the colour description says this is HDR10.
func (c ColorInfo) IsHDR() bool {
	return c.Transfer == "smpte2084" || c.Transfer == "arib-std-b67"
}

// probeOutput mirrors the parts of ffprobe's JSON that ARFABIT reads.
type probeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Index          int    `json:"index"`
		CodecName      string `json:"codec_name"`
		CodecType      string `json:"codec_type"`
		Width          int    `json:"width"`
		Height         int    `json:"height"`
		Channels       int    `json:"channels"`
		ChannelLayout  string `json:"channel_layout"`
		BitRate        string `json:"bit_rate"`
		ColorPrimaries string `json:"color_primaries"`
		ColorTransfer  string `json:"color_transfer"`
		ColorSpace     string `json:"color_space"`
		Disposition    struct {
			Default int `json:"default"`
			Forced  int `json:"forced"`
		} `json:"disposition"`
		Tags struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
		SideDataList []json.RawMessage `json:"side_data_list"`
	} `json:"streams"`
}

// Probe reads a file's stream layout and HDR metadata.
func Probe(ctx context.Context, path string) (*MediaInfo, error) {
	bin, err := LocateProbe()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, bin,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	var raw probeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("ffprobe produced output that could not be read: %w", err)
	}

	info := &MediaInfo{}
	info.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)

	for _, s := range raw.Streams {
		bitrate, _ := strconv.Atoi(s.BitRate)
		stream := Stream{
			Index:    s.Index,
			Kind:     s.CodecType,
			Codec:    s.CodecName,
			Lang:     s.Tags.Language,
			Title:    s.Tags.Title,
			Width:    s.Width,
			Height:   s.Height,
			Channels: s.Channels,
			Layout:   s.ChannelLayout,
			BitRate:  bitrate,
			Default:  s.Disposition.Default == 1,
			Forced:   s.Disposition.Forced == 1,
			ColorInfo: ColorInfo{
				Primaries: s.ColorPrimaries,
				Transfer:  s.ColorTransfer,
				Space:     s.ColorSpace,
			},
		}

		if stream.Kind == "video" {
			stream.HDR = parseHDR(s.SideDataList)
		}

		info.Streams = append(info.Streams, stream)
	}

	return info, nil
}

// VideoStream returns the first video stream, or nil.
func (m *MediaInfo) VideoStream() *Stream {
	for i := range m.Streams {
		if m.Streams[i].Kind == "video" {
			return &m.Streams[i]
		}
	}
	return nil
}

// StreamsOfKind returns every stream of a kind, in file order.
func (m *MediaInfo) StreamsOfKind(kind string) []Stream {
	var out []Stream
	for _, s := range m.Streams {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// LocateProbe finds ffprobe.
func LocateProbe() (string, error) { return locate("ffprobe") }

// Locate finds ffmpeg.
func Locate() (string, error) { return locate("ffmpeg") }

func locate(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range candidateDirs() {
		p := dir + "/" + name + exeSuffix()
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotInstalled, name)
}

func exeSuffix() string {
	if strings.HasSuffix(strings.ToLower(defaultShellExt), ".exe") {
		return ".exe"
	}
	return ""
}

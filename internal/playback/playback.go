// Package playback holds what was tested to play directly on a device, and
// says so beside a track in plain words.
//
// It informs and never decides. Whether to keep a track as it is or change it
// is the user's choice, made in a package or a blueprint; this only says what
// that choice will cost on the television. A format not listed was not
// tested, and is described as untested rather than guessed at (§4, §15).
package playback

import (
	"fmt"
	"strings"
)

// Device is one player on one piece of hardware, since what plays directly
// depends on both.
type Device string

// PlexAppleTV is the Plex app on an Apple TV 4K, tested 2026-09-25.
const PlexAppleTV Device = "Plex on Apple TV 4K"

// Result is how one format fared.
type Result int

const (
	// Untested means nobody has tried it on this device.
	Untested Result = iota

	// Direct means it reached the screen as it is, nothing converted.
	Direct

	// Converted means the server had to convert it to play it.
	Converted
)

// tested is §4's table for the Plex app on an Apple TV 4K, by stream kind and
// ffprobe codec name.
var tested = map[string]Result{
	"video/hevc": Direct,
	"video/h264": Direct,

	"audio/aac":    Direct,
	"audio/ac3":    Direct,
	"audio/eac3":   Direct,
	"audio/dts":    Direct, // DTS and DTS-HD MA share the name
	"audio/flac":   Direct,
	"audio/alac":   Direct,
	"audio/pcm":    Direct,
	"audio/truehd": Converted,

	"subtitle/subrip":            Direct,
	"subtitle/hdmv_pgs_subtitle": Converted,
}

// Check says how a stream's format fared on a device.
func Check(device Device, kind, codec string) Result {
	if device != PlexAppleTV {
		return Untested
	}
	codec = strings.ToLower(codec)
	if strings.HasPrefix(codec, "pcm_") {
		codec = "pcm"
	}
	return tested[kind+"/"+codec]
}

// Note says in a sentence what a track will cost on a device, or nothing when
// it plays directly. channels is the track's width, used to suggest a
// lossless conversion of the same width.
func Note(device Device, kind, codec string, channels int) string {
	switch Check(device, kind, codec) {
	case Direct:
		return ""
	case Converted:
		switch {
		case kind == "audio" && strings.EqualFold(codec, "truehd"):
			return fmt.Sprintf("Plex has to convert this every time it plays on Apple TV. Consider adding/substituting a converted FLAC %s that remains lossless and plays directly.", layout(channels))
		case kind == "subtitle":
			return "Plex has to convert the whole video to show these on Apple TV."
		}
		return "Plex has to convert this every time it plays on Apple TV."
	default:
		return "Not tested on Apple TV yet."
	}
}

func layout(channels int) string {
	switch {
	case channels >= 8:
		return "7.1"
	case channels == 6:
		return "5.1"
	case channels == 2:
		return "2.0"
	case channels > 0:
		return fmt.Sprintf("%d channels", channels)
	}
	return ""
}

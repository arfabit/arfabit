package pipeline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/playback"
	"github.com/arfabit/arfabit/internal/store"
)

// Track is one track an Original or a disc holds (store.Track).
type Track = store.Track

// OriginalTracks lists what an Original holds.
func OriginalTracks(info *ffmpeg.MediaInfo) []Track {
	var tracks []Track
	for _, s := range info.Streams {
		t := Track{Index: s.Index, Codec: s.Codec, Lang: s.Lang, Channels: s.Channels, Height: s.Height, Bitrate: s.BitRate}
		switch s.Kind {
		case "video":
			t.Kind = store.KindVideo
			t.HDR = s.ColorInfo.IsHDR()
		case "audio":
			t.Kind = store.KindAudio
			t.Lossless = streamLossless(s)
		case "subtitle":
			t.Kind = store.KindSubtitle
		default:
			continue
		}
		t.Label = trackText(t, s.Profile)
		t.Note = playback.Note(playback.PlexAppleTV, t.Kind, t.Codec, t.Channels)
		tracks = append(tracks, t)
	}
	return tracks
}

// trackText describes a track the way a person would.
func trackText(t Track, profile string) string {
	switch t.Kind {
	case store.KindVideo:
		name := map[string]string{"hevc": "HEVC", "h264": "H.264", "mpeg2video": "MPEG-2", "vc1": "VC-1"}[t.Codec]
		if name == "" {
			name = t.Codec
		}
		text := name
		if t.Height > 0 {
			text += " · " + heightName(t.Height)
		}
		if t.HDR {
			text += " · HDR"
		}
		return text
	case store.KindAudio:
		codec := codecName(t.Codec)
		if t.Codec == "dts" && t.Lossless {
			codec = "DTS-HD Master Audio"
		}
		// Lossless is shown as a badge beside this, from Lossless. Lossy sound
		// says its bitrate instead, which is what tells lossy tracks apart
		// and caps what they can be converted to.
		text := languageName(t.Lang) + " · " + layoutName(t.Channels, "") + " · " + codec
		if !t.Lossless && t.Bitrate > 0 {
			text += fmt.Sprintf(" · %d kbps", t.Bitrate/1000)
		}
		return text
	default:
		kind := "text"
		if t.Codec == "hdmv_pgs_subtitle" || t.Codec == "dvd_subtitle" {
			kind = "pictures"
		}
		return languageName(t.Lang) + " · " + kind
	}
}

// DiscTracks lists what a disc's title holds, from the scan, described as a
// Original's tracks are so the same package can be planned before the Original
// exists.
//
// MakeMKV lists a subtitle track's forced captions as a track of their own,
// but the Original holds them inside the one track, so they are left out here.
func DiscTracks(title disc.Title) []Track {
	var tracks []Track
	for _, s := range title.Streams {
		t := Track{Index: s.Index, Lang: s.Lang, Channels: s.Channels, Height: s.Height, Bitrate: s.Bitrate}
		switch s.Kind {
		case disc.StreamVideo:
			t.Kind, t.Codec = store.KindVideo, discVideoCodec(s.CodecID)
		case disc.StreamAudio:
			t.Kind, t.Codec = store.KindAudio, shortCodec(s.CodecID)
			t.Lossless = isLossless(s.CodecID, s.CodecLong)
		case disc.StreamSubtitle:
			if s.Forced {
				continue
			}
			t.Kind, t.Codec = store.KindSubtitle, discSubtitleCodec(s.CodecID)
		default:
			continue
		}
		t.Label = trackText(t, "")
		t.Note = playback.Note(playback.PlexAppleTV, t.Kind, t.Codec, t.Channels)
		tracks = append(tracks, t)
	}
	return tracks
}

func discVideoCodec(id string) string {
	switch id := strings.ToUpper(id); {
	case strings.Contains(id, "HEVC") || strings.Contains(id, "MPEGH"):
		return "hevc"
	case strings.Contains(id, "AVC"):
		return "h264"
	case strings.Contains(id, "MPEG2"):
		return "mpeg2video"
	case strings.Contains(id, "WVC1") || strings.Contains(id, "VC1"):
		return "vc1"
	}
	return strings.ToLower(id)
}

func discSubtitleCodec(id string) string {
	switch strings.ToUpper(id) {
	case "S_HDMV/PGS":
		return "hdmv_pgs_subtitle"
	case "S_VOBSUB":
		return "dvd_subtitle"
	case "S_TEXT/UTF8":
		return "subrip"
	}
	return strings.ToLower(id)
}

func heightName(h int) string {
	switch {
	case h >= 2000:
		return "4K"
	case h >= 1000:
		return "1080p"
	case h >= 700:
		return "720p"
	}
	return "SD"
}

// Recipe fills a Project in from a blueprint, against what the tracks are.
//
// It is only where a Project starts. The line items it makes are the same
// the user would add by hand, and they can be changed like any other. A
// blueprint asking for something the tracks do not have adds nothing for it,
// and a blueprint whose rules fit nothing at all adds no sound: the user adds
// what they want themselves, rather than ARFABIT guessing.
//
// canRead says whether this computer can read subtitles into text (§10). A
// Blu-ray's picture subtitles are converted to text where it can, unless the
// blueprint keeps them as pictures: the first track of each language, since
// each becomes a file named by its language. Any others are copied.
func Recipe(tracks []Track, b config.Blueprint, canRead bool) store.Project {
	pkg := store.Project{
		Containers: []string{"mkv"},
		Edition:    b.Edition,
		Blueprint:  b.Name,
	}

	for _, t := range tracks {
		if t.Kind == store.KindVideo {
			pkg.Items = append(pkg.Items, videoItem(t, b))
			break
		}
	}

	pkg.Items = append(pkg.Items, soundItems(tracks, b)...)

	converted := map[string]bool{}
	for _, t := range tracks {
		if t.Kind != store.KindSubtitle || !b.IncludeFullSubs || !wantLanguage(t.Lang, b.SubLanguages) {
			continue
		}
		if canRead && !b.KeepSubtitlePictures && t.Codec == pictureSubtitles && !converted[strings.ToLower(t.Lang)] {
			converted[strings.ToLower(t.Lang)] = true
			it := itemFor(t, store.ActionConvert)
			it.To = "srt"
			pkg.Items = append(pkg.Items, it)
			continue
		}
		pkg.Items = append(pkg.Items, itemFor(t, store.ActionCopy))
	}

	return pkg
}

func itemFor(t Track, action string) store.Item {
	return store.Item{
		Kind: t.Kind, Action: action, Source: t.Index,
		Codec: t.Codec, Lang: t.Lang, Channels: t.Channels, Lossless: t.Lossless, Label: t.Label,
		SourceBitrate: t.Bitrate,
	}
}

// bitrateFor is a lossy bitrate no higher than the track has: more would only
// make a bigger file of the same sound. The standard steps are used, the
// highest one that fits; a track below all of them keeps the lowest.
func bitrateFor(want string, t Track) string {
	if t.Lossless || t.Bitrate <= 0 || bitsOf(want) <= t.Bitrate {
		return want
	}
	best := bitrateSteps[0]
	for _, step := range bitrateSteps {
		if bitsOf(step) <= t.Bitrate {
			best = step
		}
	}
	return best
}

// bitrateSteps are the bitrates offered for lossy sound.
var bitrateSteps = []string{"128k", "192k", "256k", "320k", "448k", "640k", "768k"}

func bitsOf(rate string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(rate, "k"))
	return n * 1000
}

// videoItem keeps the picture or makes HEVC of it, as the blueprint says.
func videoItem(t Track, b config.Blueprint) store.Item {
	uhdCopy := t.Height >= 2000 && t.Codec == "hevc" && b.AllowUHDCopy
	if b.KeepPicture || uhdCopy {
		return itemFor(t, store.ActionCopy)
	}

	kind := "bluray"
	switch {
	case t.Height >= 2000:
		kind = "uhd"
	case t.Height > 0 && t.Height < 700:
		kind = "dvd"
	}
	it := itemFor(t, store.ActionConvert)
	it.To, it.CRF, it.Preset = "hevc", b.CRFFor(kind), b.Preset
	return it
}

// soundItems chooses the sound the way a Plan always has — the blueprint's
// rules if it has any, otherwise stereo first (§9) — and says what becomes of
// each track.
func soundItems(tracks []Track, b config.Blueprint) []store.Item {
	byIndex := map[int]Track{}
	var planned []store.PlannedAudio
	for _, t := range tracks {
		if t.Kind != store.KindAudio {
			continue
		}
		byIndex[t.Index] = t
		planned = append(planned, store.PlannedAudio{
			SourceIndex: t.Index, Lang: t.Lang, Channels: t.Channels,
			Layout: layoutName(t.Channels, ""), SourceCodec: t.Codec, Lossless: t.Lossless,
		})
	}
	if len(planned) == 0 {
		return nil
	}

	sortByLanguageThenWidth(planned, b.SubLanguages)
	if b.Sound == nil {
		selectDefaults(planned, b)
		planned = addStereoOptions(planned, b)
	} else {
		// The stereo ARFABIT could make is among what the rules choose
		// from, after anything on the disc (§8, sound rules).
		planned = addStereoOptions(planned, b)
		applySoundRules(planned, b.Sound)
	}

	var items []store.Item
	for _, p := range planned {
		if !p.Selected {
			continue
		}
		t := byIndex[p.SourceIndex]

		if p.Stereo {
			it := itemFor(t, store.ActionConvert)
			it.To, it.Bitrate, it.OutChannels = "aac", bitrateFor(b.AudioBitrate, t), 2
			items = append(items, it)
			continue
		}

		switch {
		case t.Codec == "truehd" && (b.TrueHD == config.TrueHDFLAC || b.TrueHD == config.TrueHDBoth):
			if b.TrueHD == config.TrueHDBoth {
				items = append(items, itemFor(t, store.ActionCopy))
			}
			it := itemFor(t, store.ActionConvert)
			it.To = "flac"
			items = append(items, it)

		case b.CopyNativeAudio:
			items = append(items, itemFor(t, store.ActionCopy))

		default:
			// Copying turned off: lossless becomes FLAC, lossy is converted
			// by width, as §9 describes.
			it := itemFor(t, store.ActionConvert)
			switch {
			case t.Lossless:
				it.To = "flac"
			case t.Channels > eac3MaxChannels:
				it.To, it.Bitrate = wideCodec, bitrateFor(wideBitrate, t)
			case t.Channels > 2:
				it.To, it.Bitrate = surroundCodec, bitrateFor(surroundBitrate, t)
			default:
				it.To, it.Bitrate = "aac", bitrateFor(b.AudioBitrate, t)
			}
			items = append(items, it)
		}
	}
	return items
}

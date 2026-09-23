package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/store"
)

// Surround targets. Apple TV decodes AC-3 and E-AC-3, so a track it cannot
// play is converted to one of them rather than flattened to stereo.
const (
	surroundCodec   = "eac3"
	surroundBitrate = "768k"

	// ffmpeg's E-AC-3 encoder writes at most six channels, so a 7.1 source
	// becomes 5.1. That is a real loss and the label says so.
	maxEncodedChannels = 6
)

// friendlyCodec names a codec the way a person would.
var friendlyCodec = map[string]string{
	"ac3":    "Dolby Digital",
	"eac3":   "Dolby Digital Plus",
	"aac":    "AAC",
	"truehd": "Dolby TrueHD",
	"dts":    "DTS",
	"flac":   "FLAC",
	"pcm":    "Uncompressed",
}

// friendlyLanguage names the common languages. Anything else keeps its code,
// which is better than guessing.
var friendlyLanguage = map[string]string{
	"eng": "English", "fra": "French", "fre": "French", "spa": "Spanish",
	"deu": "German", "ger": "German", "ita": "Italian", "jpn": "Japanese",
	"nld": "Dutch", "dut": "Dutch", "por": "Portuguese", "rus": "Russian",
	"kor": "Korean", "zho": "Chinese", "chi": "Chinese", "und": "Unknown",
}

// planAudio decides which sound tracks to keep and what to do with each.
//
// Tracks are grouped by language, the wanted languages first, and ordered
// widest first inside each group, so the list reads the way the disc's own
// menu would.
func planAudio(title disc.Title, profile config.Profile) []store.PlannedAudio {
	var tracks []store.PlannedAudio

	for _, s := range title.Streams {
		if s.Kind != disc.StreamAudio {
			continue
		}
		tracks = append(tracks, describeTrack(s, profile))
	}

	sortByLanguageThenWidth(tracks, profile.SubLanguages)
	selectDefaults(tracks, profile)

	return addStereoFallback(tracks, profile)
}

// addStereoFallback makes sure something stereo is always delivered.
//
// Many discs carry a stereo track already, and those are kept as they are. A
// disc that offers surround only gets a downmix made from its primary track,
// so any player and any pair of speakers has something straightforward to use.
// It goes last, because Apple TV takes the first track it understands.
func addStereoFallback(tracks []store.PlannedAudio, profile config.Profile) []store.PlannedAudio {
	var primary *store.PlannedAudio
	for i := range tracks {
		if !tracks[i].Selected {
			continue
		}
		if tracks[i].Channels <= 2 {
			// A real stereo track is already being kept.
			return tracks
		}
		if primary == nil {
			primary = &tracks[i]
		}
	}
	if primary == nil {
		return tracks
	}

	return append(tracks, store.PlannedAudio{
		SourceIndex: primary.SourceIndex,
		Codec:       "aac",
		Bitrate:     profile.AudioBitrate,
		Layout:      "Stereo",
		Channels:    2,
		Lang:        primary.Lang,
		SourceCodec: primary.SourceCodec,
		Label: fmt.Sprintf("%s · Stereo · made by ARFABIT from the %s track",
			languageName(primary.Lang), primary.Layout),
		Selected: true,
		Stereo:   true,
	})
}

// describeTrack works out what one source track becomes.
func describeTrack(s disc.Stream, profile config.Profile) store.PlannedAudio {
	source := shortCodec(s.CodecID)
	track := store.PlannedAudio{
		SourceIndex: s.Index,
		Lang:        s.Lang,
		Layout:      layoutName(s.Channels, s.Layout),
		Channels:    s.Channels,
		SourceCodec: source,
		SourceLabel: strings.TrimSpace(s.Summary),
	}

	switch {
	case profile.CopyNativeAudio && ffmpeg.CanCopyAudio(source):
		// Already something an Apple TV plays, so it passes through
		// bit-perfect at no cost.
		track.Copy = true
		track.Codec = source

	case s.Channels > 2:
		// Surround an Apple TV cannot decode is converted to Dolby Digital
		// Plus, which keeps the surround rather than flattening it.
		track.Codec = surroundCodec
		track.Bitrate = surroundBitrate
		if s.Channels > maxEncodedChannels {
			track.Downmixed = true
		}

	default:
		track.Codec = "aac"
		track.Bitrate = profile.AudioBitrate
	}

	track.Label = trackLabel(track)
	return track
}

// trackLabel describes a track in one line: what it is, and what becomes of it.
func trackLabel(t store.PlannedAudio) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s · %s · %s", languageName(t.Lang), t.Layout, codecName(t.SourceCodec))

	switch {
	case t.Copy:
		b.WriteString(" — kept exactly as it is")
	case t.Downmixed:
		fmt.Fprintf(&b, " — converted to %s 5.1 (from %s)", codecName(t.Codec), t.Layout)
	case t.Channels > 2:
		fmt.Fprintf(&b, " — converted to %s %s", codecName(t.Codec), t.Layout)
	default:
		fmt.Fprintf(&b, " — converted to %s", codecName(t.Codec))
	}

	return b.String()
}

// sortByLanguageThenWidth groups the list the way a disc menu would.
func sortByLanguageThenWidth(tracks []store.PlannedAudio, wanted []string) {
	rank := map[string]int{}
	for i, lang := range wanted {
		rank[strings.ToLower(lang)] = i
	}

	languageRank := func(lang string) int {
		if r, ok := rank[strings.ToLower(lang)]; ok {
			return r
		}
		return len(wanted) + 1
	}

	sort.SliceStable(tracks, func(a, b int) bool {
		ra, rb := languageRank(tracks[a].Lang), languageRank(tracks[b].Lang)
		if ra != rb {
			return ra < rb
		}
		if tracks[a].Lang != tracks[b].Lang {
			return tracks[a].Lang < tracks[b].Lang
		}
		return tracks[a].Channels > tracks[b].Channels
	})
}

// selectDefaults ticks the tracks a person would most likely want.
//
// The widest track in the first wanted language, plus every stereo track in
// that language. Discs often carry two stereo tracks with nothing to tell them
// apart — one is frequently a commentary — and since ARFABIT cannot know
// which is which, it keeps both rather than choosing wrongly.
func selectDefaults(tracks []store.PlannedAudio, profile config.Profile) {
	primaryLang := ""
	if len(profile.SubLanguages) > 0 {
		primaryLang = strings.ToLower(profile.SubLanguages[0])
	}

	inLanguage := func(t store.PlannedAudio) bool {
		return primaryLang == "" || strings.EqualFold(t.Lang, primaryLang)
	}

	widest := -1
	for i, t := range tracks {
		if !inLanguage(t) {
			continue
		}
		if widest < 0 || t.Channels > tracks[widest].Channels {
			widest = i
		}
	}
	if widest < 0 {
		// Nothing in the wanted language, so keep the widest of whatever
		// there is rather than delivering a silent film.
		for i, t := range tracks {
			if widest < 0 || t.Channels > tracks[widest].Channels {
				widest = i
			}
		}
	}
	if widest < 0 {
		return
	}

	tracks[widest].Selected = true

	for i := range tracks {
		if inLanguage(tracks[i]) && tracks[i].Channels <= 2 {
			tracks[i].Selected = true
		}
	}
}

// layoutName describes a channel layout plainly.
//
// MakeMKV reports layouts like "5.1(side)", which is accurate and means
// nothing to most people.
func layoutName(channels int, raw string) string {
	switch {
	case channels >= 8:
		return "7.1"
	case channels == 7:
		return "6.1"
	case channels == 6:
		return "5.1"
	case channels == 2:
		return "Stereo"
	case channels == 1:
		return "Mono"
	}
	if raw != "" {
		return raw
	}
	return "Unknown"
}

func codecName(codec string) string {
	if name, ok := friendlyCodec[strings.ToLower(codec)]; ok {
		return name
	}
	return strings.ToUpper(codec)
}

func languageName(lang string) string {
	if name, ok := friendlyLanguage[strings.ToLower(lang)]; ok {
		return name
	}
	if lang == "" {
		return "Unknown"
	}
	return strings.ToUpper(lang)
}

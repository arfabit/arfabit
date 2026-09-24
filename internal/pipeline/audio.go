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

// Surround targets.
//
// Apple TV decodes AC-3, E-AC-3 and AAC, so surround it cannot play is
// converted to one of them rather than flattened to stereo. Which one depends
// on how wide the source is, because the encoders differ:
//
//   - E-AC-3 is the better target. A receiver can take the bitstream whole,
//     and it is what streaming services ship. But ffmpeg's encoder implements
//     only up to 5.1 — the format itself allows 7.1 — and it downmixes a wider
//     source silently.
//   - AAC handles all eight channels, so it is used for 7.1 sources, where
//     keeping every channel matters more than being bitstreamable.
const (
	// surroundCodec is used for sources up to 5.1.
	surroundCodec   = "eac3"
	surroundBitrate = "768k"

	// wideCodec is used for sources wider than E-AC-3's encoder can manage.
	wideCodec   = "aac"
	wideBitrate = "640k"

	// eac3MaxChannels is ffmpeg's E-AC-3 encoder limit, not the format's.
	eac3MaxChannels = 6
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

// losslessCodecs are the formats that carry the studio's audio bit for bit.
//
// DTS-HD Master Audio is the awkward one: MakeMKV reports it with the same
// codec id as ordinary DTS and distinguishes it only in the long name.
var losslessCodecs = map[string]bool{
	"truehd": true,
	"flac":   true,
	"pcm":    true,
}

// isLossless reports whether a track carries the audio without loss.
func isLossless(codecID, codecLong string) bool {
	if losslessCodecs[strings.ToLower(shortCodec(codecID))] {
		return true
	}
	long := strings.ToLower(codecLong)
	return strings.Contains(long, "master audio") || strings.Contains(long, "lossless")
}

// friendlyLanguage names the common languages. Anything else keeps its code,
// which is better than guessing.
var friendlyLanguage = map[string]string{
	"eng": "English", "fra": "French", "fre": "French", "spa": "Spanish",
	"deu": "German", "ger": "German", "ita": "Italian", "jpn": "Japanese",
	"nld": "Dutch", "dut": "Dutch", "por": "Portuguese", "rus": "Russian",
	"kor": "Korean", "zho": "Chinese", "chi": "Chinese", "und": "Unknown",
}

// planAudio decides which sound tracks to offer and what each would become.
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

	return addStereoOptions(tracks, profile)
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
		Lossless:    isLossless(s.CodecID, s.CodecLong),
	}

	switch {
	case profile.CopyNativeAudio && ffmpeg.CanCopyAudio(source):
		// Already something an Apple TV plays, so it passes through
		// bit-perfect at no cost.
		track.Copy = true
		track.Codec = source

	case s.Channels > eac3MaxChannels:
		// Wider than E-AC-3's encoder manages, so AAC keeps every channel.
		track.Codec = wideCodec
		track.Bitrate = wideBitrate

	case s.Channels > 2:
		track.Codec = surroundCodec
		track.Bitrate = surroundBitrate

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
	if t.Lossless {
		b.WriteString(" (lossless)")
	}

	switch {
	case t.Copy:
		b.WriteString(" — kept exactly as it is")
	case t.Channels > 2:
		fmt.Fprintf(&b, " — converted to %s %s, all channels kept", codecName(t.Codec), t.Layout)
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

// selectDefaults ticks the tracks a person gets without choosing anything.
//
// Stereo only, for now. It plays on everything, needs no receiver, and is the
// least surprising thing to find on the television. Every surround track is
// listed alongside with what it would become, so turning one on is one click —
// the Plan shows the choice rather than making it.
func selectDefaults(tracks []store.PlannedAudio, profile config.Profile) {
	inLanguage := languageFilter(profile)

	// Discs often carry two stereo tracks with nothing to tell them apart —
	// one is frequently a commentary. ARFABIT cannot know which, so it keeps
	// both rather than choosing wrongly.
	var found bool
	for i := range tracks {
		if inLanguage(tracks[i].Lang) && tracks[i].Channels <= 2 {
			tracks[i].Selected = true
			found = true
		}
	}
	if found {
		return
	}

	// Nothing stereo in the wanted language, so fall back to any stereo track
	// at all before resorting to a downmix.
	for i := range tracks {
		if tracks[i].Channels <= 2 {
			tracks[i].Selected = true
			return
		}
	}
}

// addStereoOptions makes sure stereo is always available, and offers a second
// way of getting it when the disc has a lossless track.
//
// A disc's own stereo track is usually a purpose-made mix, and that is what
// gets ticked. But it is also typically Dolby at a few hundred kilobits, while
// the surround track beside it may be lossless — so a downmix from that is
// offered as well, unticked, for anyone who would rather have the better source
// than the studio's fold-down. Neither is obviously right, which is why both
// are on the list.
func addStereoOptions(tracks []store.PlannedAudio, profile config.Profile) []store.PlannedAudio {
	inLanguage := languageFilter(profile)

	var (
		haveStereo   bool
		widest       *store.PlannedAudio
		bestLossless *store.PlannedAudio
	)
	for i := range tracks {
		t := &tracks[i]
		if t.Channels <= 2 {
			haveStereo = true
			continue
		}
		if !inLanguage(t.Lang) {
			continue
		}
		if widest == nil || t.Channels > widest.Channels {
			widest = t
		}
		if t.Lossless && (bestLossless == nil || t.Channels > bestLossless.Channels) {
			bestLossless = t
		}
	}

	// Nothing stereo on the disc at all, so one is made from the best source
	// available: lossless where the disc has it.
	if !haveStereo {
		source := bestLossless
		if source == nil {
			source = widest
		}
		if source == nil && len(tracks) > 0 {
			source = &tracks[0]
		}
		if source == nil {
			return tracks
		}
		return append(tracks, derivedStereo(*source, profile, true))
	}

	// A stereo track exists, but a lossless surround track is a better source
	// for anyone who prefers it to the disc's own mix.
	if bestLossless != nil {
		return append(tracks, derivedStereo(*bestLossless, profile, false))
	}

	return tracks
}

// derivedStereo describes a stereo track made from a wider one.
func derivedStereo(source store.PlannedAudio, profile config.Profile, selected bool) store.PlannedAudio {
	quality := "the"
	if source.Lossless {
		quality = "the lossless"
	}

	return store.PlannedAudio{
		SourceIndex: source.SourceIndex,
		Codec:       "aac",
		Bitrate:     profile.AudioBitrate,
		Layout:      "Stereo",
		Channels:    2,
		Lang:        source.Lang,
		SourceCodec: source.SourceCodec,
		Lossless:    source.Lossless,
		Label: fmt.Sprintf("%s · Stereo · made by ARFABIT from %s %s %s track",
			languageName(source.Lang), quality, source.Layout, codecName(source.SourceCodec)),
		Selected: selected,
		Stereo:   true,
	}
}

// languageFilter reports whether a track is in the language the user wants.
// With no languages configured, every track counts.
func languageFilter(profile config.Profile) func(string) bool {
	if len(profile.SubLanguages) == 0 {
		return func(string) bool { return true }
	}
	primary := strings.ToLower(profile.SubLanguages[0])
	return func(lang string) bool { return strings.EqualFold(lang, primary) }
}

// DescribeStream names a track from a master in plain words.
//
// What a person needs in order to choose between tracks is the language, how
// wide it is and what it is — not a codec identifier.
func DescribeStream(s ffmpeg.Stream) string {
	name := languageName(s.Lang)

	switch s.Kind {
	case "audio":
		label := fmt.Sprintf("%s · %s · %s", name, layoutName(s.Channels, s.Layout), codecName(s.Codec))
		if isLossless("", s.Codec) {
			label += " (lossless)"
		}
		if s.Title != "" {
			label += " · " + s.Title
		}
		return label

	case "subtitle":
		label := name
		if s.Forced {
			label += " · only for foreign speech"
		}
		if s.Title != "" {
			label += " · " + s.Title
		}
		return label
	}

	return name
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

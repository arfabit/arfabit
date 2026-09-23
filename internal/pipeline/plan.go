// Package pipeline turns a scanned disc into a finished file.
package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/store"
)

// BuildPlan works out what to do with a disc.
//
// Everything it decides is shown to the user before anything happens, and
// every choice can be changed. Nothing here acts on its own (§8).
func BuildPlan(d *disc.Disc, sel disc.Selection, profile config.Profile) (*store.Plan, error) {
	if sel.Feature < 0 || sel.Feature >= len(d.Titles) {
		return nil, fmt.Errorf("no title was selected")
	}
	title := d.Titles[sel.Feature]

	plan := &store.Plan{
		Profile:    profile.Name,
		TitleIndex: title.Index,
		Duration:   formatDuration(title.Duration),
		SourceSize: title.SizeBytes,
		Obfuscated: sel.Obfuscated,
		Reason:     sel.Reason,
	}

	video := findVideo(title)
	if video == nil {
		return nil, fmt.Errorf("this title has no picture")
	}

	plan.SourceCodec = video.CodecLong
	plan.Resolution = fmt.Sprintf("%dx%d", video.Width, video.Height)
	planVideo(plan, d.Kind, video, profile)
	plan.Audio = planAudio(title, profile)
	plan.Subtitles = planSubtitles(title, profile)

	return plan, nil
}

// planVideo decides whether to copy or re-encode the picture.
func planVideo(plan *store.Plan, kind disc.Kind, video *disc.Stream, profile config.Profile) {
	plan.VideoCodec = "hevc"
	plan.CRF = profile.CRFFor(string(kind))
	plan.Preset = profile.Preset

	// A UHD disc is already HEVC, so copying it is free and bit-perfect.
	// Re-encoding it is lossy-to-lossy, which is offered but not assumed.
	if kind == disc.KindUHD && profile.AllowUHDCopy && isHEVC(video.CodecID) {
		plan.VideoCopy = true
	}
}

func isHEVC(codecID string) bool {
	id := strings.ToUpper(codecID)
	return strings.Contains(id, "HEVC") || strings.Contains(id, "H265") || strings.Contains(id, "MPEGH")
}

// planSubtitles selects subtitle tracks in the wanted languages.
//
// Forced tracks default to on: on most discs they carry the translations for
// scenes in another language, which a viewer wants without asking (§10).
func planSubtitles(title disc.Title, profile config.Profile) []store.PlannedSubtitle {
	var planned []store.PlannedSubtitle

	for _, s := range title.Streams {
		if s.Kind != disc.StreamSubtitle {
			continue
		}
		wanted := wantLanguage(s.Lang, profile.SubLanguages)
		selected := wanted && ((s.Forced && profile.IncludeForcedSubs) || (!s.Forced && profile.IncludeFullSubs))

		planned = append(planned, store.PlannedSubtitle{
			SourceIndex: s.Index,
			Lang:        s.Lang,
			Forced:      s.Forced,
			Label:       strings.TrimSpace(s.Summary),
			Selected:    selected,
		})
	}

	return planned
}

// wantLanguage reports whether a track's language is one the user asked for.
// An empty list means every language.
func wantLanguage(lang string, wanted []string) bool {
	if len(wanted) == 0 {
		return true
	}
	for _, w := range wanted {
		if strings.EqualFold(lang, w) {
			return true
		}
	}
	return false
}

// shortCodec turns a Matroska codec id into the name ffmpeg uses.
func shortCodec(codecID string) string {
	switch strings.ToUpper(strings.TrimPrefix(codecID, "A_")) {
	case "AC3":
		return "ac3"
	case "EAC3":
		return "eac3"
	case "AAC":
		return "aac"
	case "TRUEHD":
		return "truehd"
	case "DTS":
		return "dts"
	case "FLAC":
		return "flac"
	default:
		return strings.ToLower(strings.TrimPrefix(codecID, "A_"))
	}
}

func findVideo(title disc.Title) *disc.Stream {
	for i := range title.Streams {
		if title.Streams[i].Kind == disc.StreamVideo {
			return &title.Streams[i]
		}
	}
	return nil
}

// formatDuration renders a duration the way a person reads it.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "unknown"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

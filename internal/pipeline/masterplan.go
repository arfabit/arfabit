package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/store"
)

// planFromMaster works out what to do with a copy, from the copy itself.
//
// There is no disc to ask, so everything comes from what is in the file. That
// is the better source anyway: the copy is what gets converted.
func planFromMaster(ctx context.Context, master string, profile config.Profile) (*store.Plan, error) {
	info, err := ffmpeg.Probe(ctx, master)
	if err != nil {
		return nil, fmt.Errorf("the copy could not be read: %w", err)
	}

	video := info.VideoStream()
	if video == nil {
		return nil, fmt.Errorf("the copy has no picture in it")
	}

	// A copy from a UHD disc is already HEVC; anything else is converted.
	kind := disc.KindBluray
	if video.Height > 1500 {
		kind = disc.KindUHD
	} else if video.Height < 800 {
		kind = disc.KindDVD
	}

	plan := &store.Plan{
		Profile:     profile.Name,
		Convert:     true,
		VideoCodec:  "hevc",
		CRF:         profile.CRFFor(string(kind)),
		Preset:      profile.Preset,
		SourceCodec: video.Codec,
		Resolution:  fmt.Sprintf("%dx%d", video.Width, video.Height),
		HDR:         video.ColorInfo.IsHDR(),
		Duration:    formatDuration(time.Duration(info.Duration * float64(time.Second))),
	}

	if kind == disc.KindUHD && profile.AllowUHDCopy && video.Codec == "hevc" {
		plan.VideoCopy = true
	}

	// The copy's own streams are what there is to choose from.
	for _, s := range info.StreamsOfKind("audio") {
		plan.Audio = append(plan.Audio, describeMasterTrack(s, profile))
	}
	sortByLanguageThenWidth(plan.Audio, profile.SubLanguages)
	selectDefaults(plan.Audio, profile)
	plan.Audio = addStereoOptions(plan.Audio, profile)

	return plan, nil
}

// describeMasterTrack is describeTrack for a stream read from a copy rather
// than reported by a disc.
func describeMasterTrack(s ffmpeg.Stream, profile config.Profile) store.PlannedAudio {
	track := store.PlannedAudio{
		SourceIndex: s.Index,
		Lang:        s.Lang,
		Layout:      layoutName(s.Channels, s.Layout),
		Channels:    s.Channels,
		SourceCodec: s.Codec,
		SourceLabel: s.Title,
		Lossless:    isLossless("", s.Codec),
	}

	switch {
	case profile.CopyNativeAudio && ffmpeg.CanCopyAudio(s.Codec):
		track.Copy = true
		track.Codec = s.Codec
	case s.Channels > eac3MaxChannels:
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

package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/store"
)

// ConvertRequest is a copy to be turned into a film.
type ConvertRequest struct {
	// Master is the copy to work from.
	Master string

	// Title and Year name the film. Taken from the copy's folder when empty.
	Title string
	Year  int
}

// StartConvert makes a film from a copy that already exists.
//
// This is the other half of being able to stop after copying: a disc can be
// read and set aside, and turned into a film whenever there is time, without
// the disc ever being needed again.
func (r *Runner) StartConvert(parent context.Context, req ConvertRequest) (*Job, error) {
	if req.Master == "" {
		return nil, fmt.Errorf("there is no copy to convert")
	}

	title := req.Title
	if title == "" {
		// The copy's folder is named after the film, which is what makes this
		// work without being told.
		title = filepath.Base(filepath.Dir(req.Master))
	}

	rec := store.NewJob(store.NewJobID(time.Now(), title))
	rec.Kind = store.KindConvert
	rec.Title = title
	rec.Year = req.Year
	rec.Master = req.Master
	rec.Stage = store.StagePackage

	log, err := NewLog(r.Store.LogPath(rec.ID), func(e Entry) {
		if r.OnLog != nil {
			r.OnLog(e)
		}
	})
	if err != nil {
		return nil, err
	}
	log.Describe(rec.ID, title)

	plan, err := planFromMaster(parent, req.Master, r.Config.Profile)
	if err != nil {
		return nil, err
	}
	rec.Plan = plan

	ctx, cancel := context.WithCancel(parent)
	job := &Job{Job: rec, Log: log, cancel: cancel}
	job.Progress = Progress{Since: time.Now(), Operation: "Making the movie file"}

	r.begin(job)
	r.save(job)

	go func() {
		defer cancel()
		defer r.finish(job)
		r.runConvert(ctx, job)
	}()

	return job, nil
}

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

// runConvert makes the film.
func (r *Runner) runConvert(ctx context.Context, job *Job) {
	if r.Slots != nil {
		if running, _ := r.Slots.Busy(); running > 0 {
			job.Stage = store.StageQueued
			job.Progress = Progress{Since: time.Now(), Operation: "Waiting for a turn"}
			job.Log.Printf(store.StageQueued, "Waiting to convert: something else is using the processor.")
			r.save(job)
		}
		if err := r.Slots.Take(ctx); err != nil {
			r.stop(job, "Stopped while waiting to convert.", "")
			return
		}
		defer r.Slots.Give()
	}

	job.Stage = store.StagePackage
	job.Progress = Progress{Since: time.Now(), Operation: "Making the movie file"}
	job.Log.Printf(store.StagePackage, "Making the movie file from %s.", filepath.Base(job.Master))
	r.save(job)

	title := meta.Title{Name: job.Title, Year: job.Year}

	delivery, err := r.packageMaster(ctx, job, title)
	if err != nil {
		r.stop(job, "ARFABIT did not finish making the movie file.", detailOf(err))
		return
	}
	job.Delivery = delivery

	job.Stage = store.StageDeliver
	if err := r.deliver(job, title); err != nil {
		r.stop(job, "ARFABIT made the movie but could not put it in your library.", err.Error())
		return
	}

	job.State = store.StateDone
	job.Progress = Progress{Percent: 100}
	job.Note = fmt.Sprintf("%s is ready.", job.Title)
	r.save(job)
}

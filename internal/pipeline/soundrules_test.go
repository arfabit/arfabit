package pipeline

import (
	"slices"
	"testing"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/ffmpeg"
	"github.com/arfabit/arfabit/internal/store"
)

// A disc like The Mandalorian and Grogu: lossless 7.1, a lossy 5.1 core, two
// stereo tracks, French and Spanish surround, and the stereo ARFABIT would
// make from the lossless track.
func mandalorian() []store.PlannedAudio {
	return []store.PlannedAudio{
		{SourceIndex: 1, Lang: "eng", Channels: 8, Lossless: true, SourceCodec: "dts"},
		{SourceIndex: 2, Lang: "eng", Channels: 6, SourceCodec: "dts"},
		{SourceIndex: 3, Lang: "eng", Channels: 2, SourceCodec: "ac3"},
		{SourceIndex: 6, Lang: "eng", Channels: 2, SourceCodec: "ac3"},
		{SourceIndex: 4, Lang: "fra", Channels: 6, SourceCodec: "ac3"},
		{SourceIndex: 5, Lang: "spa", Channels: 6, SourceCodec: "ac3"},
		{SourceIndex: 1, Lang: "eng", Channels: 2, Lossless: true, Stereo: true},
	}
}

func chosen(tracks []store.PlannedAudio) []int {
	var out []int
	for i, t := range tracks {
		if t.Selected {
			out = append(out, i)
		}
	}
	return out
}

func TestSoundRules(t *testing.T) {
	bestSurround := config.SoundChoice{Mode: config.SoundOne, Layouts: []string{"7.1", "5.1"}, Quality: "lossless"}
	anyStereo := config.SoundChoice{Mode: config.SoundOne, Layouts: []string{"stereo"}}

	for _, tc := range []struct {
		name   string
		tracks []store.PlannedAudio
		rules  config.SoundRules
		want   []int // positions in tracks
		found  bool
	}{
		{
			name:   "best lossless surround and a stereo, in English",
			tracks: mandalorian(),
			rules:  config.SoundRules{Languages: []string{"eng"}, Choices: []config.SoundChoice{bestSurround, anyStereo}},
			// The disc's own stereo comes before one ARFABIT would make.
			want:  []int{0, 2},
			found: true,
		},
		{
			name: "falls back to the next layout named",
			tracks: []store.PlannedAudio{
				{Lang: "eng", Channels: 6, Lossless: true},
				{Lang: "eng", Channels: 2},
			},
			rules: config.SoundRules{Languages: []string{"eng"}, Choices: []config.SoundChoice{bestSurround}},
			want:  []int{0},
			found: true,
		},
		{
			name:   "keep all of every track in two languages",
			tracks: mandalorian(),
			rules: config.SoundRules{Languages: []string{"eng", "fra"}, LanguageMode: config.SoundAll,
				Choices: []config.SoundChoice{{Mode: config.SoundAll}}},
			want:  []int{0, 1, 2, 3, 4, 6},
			found: true,
		},
		{
			name:   "one of several languages takes the first the disc has",
			tracks: mandalorian(),
			rules: config.SoundRules{Languages: []string{"jpn", "spa", "fra"}, LanguageMode: config.SoundOne,
				Choices: []config.SoundChoice{{Mode: config.SoundAll}}},
			want:  []int{5},
			found: true,
		},
		{
			name:   "every language on the disc when none is named",
			tracks: mandalorian(),
			rules:  config.SoundRules{Choices: []config.SoundChoice{{Mode: config.SoundOne, Layouts: []string{"5.1"}, Quality: "lossy"}}},
			want:   []int{1, 4, 5},
			found:  true,
		},
		{
			name:   "a stereo ARFABIT makes is never lossless",
			tracks: []store.PlannedAudio{{Lang: "eng", Channels: 8, Lossless: true}, {Lang: "eng", Channels: 2, Lossless: true, Stereo: true}},
			rules:  config.SoundRules{Choices: []config.SoundChoice{{Mode: config.SoundOne, Layouts: []string{"stereo"}, Quality: "lossless"}}},
			found:  false,
		},
		{
			name:   "nothing asked for is on the disc",
			tracks: mandalorian(),
			rules:  config.SoundRules{Languages: []string{"jpn"}, Choices: []config.SoundChoice{anyStereo}},
			found:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks := tc.tracks
			tracks[0].Selected = true // whatever was ticked before is replaced

			outcomes, found := applySoundRules(tracks, &tc.rules)
			if found != tc.found {
				t.Errorf("found = %v, want %v", found, tc.found)
			}
			if got := chosen(tracks); !slices.Equal(got, tc.want) {
				t.Errorf("chose %v, want %v", got, tc.want)
			}
			if len(outcomes) == 0 {
				t.Error("nothing was reported for the Plan to show")
			}
		})
	}
}

// A rule that finds nothing is reported as such, while the others still count.
func TestSoundRuleThatFindsNothingIsReported(t *testing.T) {
	tracks := []store.PlannedAudio{{Lang: "eng", Channels: 2, Source: "English · Stereo · Dolby Digital · lossy"}}
	rules := config.SoundRules{Languages: []string{"eng"}, Choices: []config.SoundChoice{
		{Mode: config.SoundOne, Layouts: []string{"7.1", "5.1"}, Quality: "lossless"},
		{Mode: config.SoundOne, Layouts: []string{"stereo"}},
	}}

	outcomes, found := applySoundRules(tracks, &rules)
	if !found {
		t.Fatal("the stereo track was not found")
	}
	if len(outcomes) != 2 || len(outcomes[0].Matched) != 0 || len(outcomes[1].Matched) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if outcomes[0].Choice != "pick one of 7.1 or 5.1, lossless" {
		t.Errorf("choice read as %q", outcomes[0].Choice)
	}
	if outcomes[1].Matched[0] != "Stereo · Dolby Digital · lossy" {
		t.Errorf("matched read as %q", outcomes[1].Matched[0])
	}
}

// A master's lossless tracks are recognised as lossless. They were not: the
// check was handed the codec where it expected a disc's codec id, so even
// TrueHD read as lossy.
func TestMasterTracksKnowTheyAreLossless(t *testing.T) {
	for _, tc := range []struct {
		stream ffmpeg.Stream
		want   bool
	}{
		{ffmpeg.Stream{Codec: "truehd"}, true},
		{ffmpeg.Stream{Codec: "dts", Profile: "DTS-HD MA"}, true},
		{ffmpeg.Stream{Codec: "pcm_s24le"}, true},
		{ffmpeg.Stream{Codec: "dts", Profile: "DTS"}, false},
		{ffmpeg.Stream{Codec: "ac3"}, false},
	} {
		if got := streamLossless(tc.stream); got != tc.want {
			t.Errorf("%s %s lossless = %v, want %v", tc.stream.Codec, tc.stream.Profile, got, tc.want)
		}
	}
}

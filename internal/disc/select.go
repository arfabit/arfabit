package disc

import (
	"fmt"
	"sort"
	"time"
)

// Selection is what a scan concluded about which titles matter.
//
// Nothing here is acted on without the user seeing it. The Plan shows the
// choice and the reasoning, and the user can change it (§8).
type Selection struct {
	// Feature is the index of the suggested main feature, or -1 when no title
	// stands out enough to suggest one.
	Feature int

	// Extras are other titles long enough to be real content: bonus features,
	// or the episodes of a TV disc.
	Extras []int

	// Obfuscated reports that the disc appears to be hiding its main feature
	// among near-identical decoys. The Plan says so plainly rather than
	// silently guessing.
	Obfuscated bool

	// Reason is a short plain-language explanation, shown to the user.
	Reason string
}

// minFeatureDuration is the shortest title that could plausibly be a feature
// or a TV episode. Below this a title is a menu, a logo or a stinger.
const minFeatureDuration = 15 * time.Minute

// decoyTolerance is how close two titles must be to count as the same title.
//
// Real playlist decoys are byte-for-byte the same content wrapped in different
// playlists, so their durations match exactly. An alternate cut differs by
// minutes. A one-second window separates the two without catching cuts.
const decoyTolerance = time.Second

// minDecoys is how many identical-length long titles constitute obfuscation.
//
// Observed on a real disc: three titles at exactly 2h12m05s and 40.7 GB, in
// playlists 00800, 00802 and 00803. Two identical titles is common enough to be
// unremarkable; three is deliberate.
const minDecoys = 3

// SelectTitles suggests which titles to rip.
//
// The heuristic is longest-wins, with two refinements: titles too short to be
// content are ignored, and a cluster of near-identical long titles is reported
// as obfuscation rather than resolved by guessing.
func SelectTitles(titles []Title) Selection {
	candidates := make([]int, 0, len(titles))
	for i, t := range titles {
		if t.Duration >= minFeatureDuration {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		return Selection{
			Feature: -1,
			Reason:  "No title on this disc is long enough to be a movie.",
		}
	}

	// Longest first, breaking ties by size so the higher-bitrate copy of two
	// equally long titles wins.
	sort.SliceStable(candidates, func(a, b int) bool {
		ta, tb := titles[candidates[a]], titles[candidates[b]]
		if ta.Duration != tb.Duration {
			return ta.Duration > tb.Duration
		}
		return ta.SizeBytes > tb.SizeBytes
	})

	longest := titles[candidates[0]]
	decoys := countNearIdentical(titles, candidates, longest.Duration)

	sel := Selection{Feature: candidates[0]}
	for _, i := range candidates[1:] {
		sel.Extras = append(sel.Extras, i)
	}

	switch {
	case decoys >= minDecoys:
		sel.Obfuscated = true
		sel.Reason = fmt.Sprintf("This disc lists %d titles of exactly the same length, "+
			"which some discs do to make the movie harder to find. "+
			"They are usually the same film, so any of them works. "+
			"The first is selected.", decoys)
	case len(candidates) == 1:
		sel.Reason = "One title on this disc is long enough to be the movie."
	default:
		sel.Reason = "The longest title is selected as the movie."
	}

	return sel
}

// countNearIdentical counts candidates whose duration matches the longest
// within decoyTolerance.
func countNearIdentical(titles []Title, candidates []int, longest time.Duration) int {
	if longest <= 0 {
		return 0
	}

	n := 0
	for _, i := range candidates {
		diff := longest - titles[i].Duration
		if diff < 0 {
			diff = -diff
		}
		if diff <= decoyTolerance {
			n++
		}
	}
	return n
}

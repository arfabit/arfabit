package disc

import (
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

// decoyTolerance is how close in duration two titles must be to count as
// near-identical. Playlist obfuscation produces titles within a second or two
// of each other; genuine alternate cuts differ by minutes.
const decoyTolerance = 0.02 // 2%

// minDecoys is how many near-identical long titles constitute obfuscation.
// Two similar titles is ordinary (a film and its alternate cut); five is a
// deliberate attempt to hide the feature.
const minDecoys = 5

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
		sel.Reason = "This disc lists several titles of almost the same length, " +
			"which some discs do to make the movie harder to find. " +
			"The longest one is selected, but it is worth checking."
	case len(candidates) == 1:
		sel.Reason = "One title on this disc is long enough to be the movie."
	default:
		sel.Reason = "The longest title is selected as the movie."
	}

	return sel
}

// countNearIdentical counts candidates whose duration is within decoyTolerance
// of the longest.
func countNearIdentical(titles []Title, candidates []int, longest time.Duration) int {
	if longest <= 0 {
		return 0
	}

	n := 0
	for _, i := range candidates {
		d := titles[i].Duration
		diff := longest - d
		if diff < 0 {
			diff = -diff
		}
		if float64(diff)/float64(longest) <= decoyTolerance {
			n++
		}
	}
	return n
}

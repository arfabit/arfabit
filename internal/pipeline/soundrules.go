package pipeline

import (
	"slices"
	"strings"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/store"
)

// applyBlueprintSound applies a blueprint's sound rules to a Plan, if it has
// any. Without them the Plan keeps the stereo-first choice already made.
func applyBlueprintSound(plan *store.Plan, blueprint config.Blueprint) {
	if blueprint.Sound == nil {
		return
	}
	var found bool
	plan.Sound, found = applySoundRules(plan.Audio, blueprint.Sound)
	plan.SoundNotFound = !found
}

// applySoundRules ticks the sound tracks a blueprint's rules ask for, and
// says what each rule found.
//
// Rules describe tracks by what they are — language, layout, lossless or not
// — because the same blueprint meets discs that carry different things. A
// rule that finds nothing on this disc is reported and passed over. Only when
// every rule finds nothing does it matter, and then found is false and the
// choice is left to the user: a blueprint that fits nothing on a disc should
// not quietly produce a film with no sound.
func applySoundRules(tracks []store.PlannedAudio, rules *config.SoundRules) (outcomes []store.SoundOutcome, found bool) {
	for i := range tracks {
		tracks[i].Selected = false
	}

	languages := ruleLanguages(tracks, rules)
	if len(languages) == 0 {
		names := make([]string, len(rules.Languages))
		for i, code := range rules.Languages {
			names[i] = languageName(code)
		}
		return []store.SoundOutcome{{Language: strings.Join(names, " or "), Choice: "any audio"}}, false
	}

	for _, lang := range languages {
		for _, choice := range rules.Choices {
			outcome := store.SoundOutcome{Language: languageName(lang), Choice: DescribeSoundChoice(choice)}

			for _, i := range candidates(tracks, lang, choice) {
				tracks[i].Selected = true
				found = true
				outcome.Matched = append(outcome.Matched, trackDescription(tracks[i]))
				if choice.Mode != config.SoundAll {
					break
				}
			}
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, found
}

// anySelected reports whether any sound track is ticked.
func anySelected(tracks []store.PlannedAudio) bool {
	for _, t := range tracks {
		if t.Selected {
			return true
		}
	}
	return false
}

// ruleLanguages is which languages the rules apply to on this disc, in the
// order the rules prefer them.
func ruleLanguages(tracks []store.PlannedAudio, rules *config.SoundRules) []string {
	var onDisc []string
	for _, t := range tracks {
		if !slices.Contains(onDisc, t.Lang) {
			onDisc = append(onDisc, t.Lang)
		}
	}
	if len(rules.Languages) == 0 {
		return onDisc
	}

	var wanted []string
	for _, code := range rules.Languages {
		for _, lang := range onDisc {
			if sameLanguage(lang, code) && !slices.Contains(wanted, lang) {
				wanted = append(wanted, lang)
			}
		}
		if len(wanted) > 0 && rules.LanguageMode != config.SoundAll {
			break
		}
	}
	return wanted
}

// candidates are the tracks in one language that fit a choice, best first:
// the layouts in the order the choice names them, and within a layout, what
// is on the disc before a stereo track ARFABIT would make itself.
func candidates(tracks []store.PlannedAudio, lang string, choice config.SoundChoice) []int {
	var fit []int
	for i, t := range tracks {
		if t.Lang == lang && fitsLayout(t, choice.Layouts) && fitsQuality(t, choice.Quality) {
			fit = append(fit, i)
		}
	}

	rank := func(i int) int {
		r := slices.Index(choice.Layouts, layoutOf(tracks[i]))
		if r < 0 {
			r = 0
		}
		r *= 2
		if tracks[i].Stereo {
			r++
		}
		return r
	}
	slices.SortStableFunc(fit, func(a, b int) int { return rank(a) - rank(b) })
	return fit
}

// layoutOf sorts a track into the three layouts a rule can name.
func layoutOf(t store.PlannedAudio) string {
	switch {
	case t.Channels >= 7:
		return "7.1"
	case t.Channels >= 3:
		return "5.1"
	default:
		return "stereo"
	}
}

func fitsLayout(t store.PlannedAudio, layouts []string) bool {
	return len(layouts) == 0 || slices.Contains(layouts, layoutOf(t))
}

// fitsQuality matches lossless and lossy. A stereo track ARFABIT makes itself
// is an encode, so it is lossy whatever it was made from.
func fitsQuality(t store.PlannedAudio, quality string) bool {
	lossless := t.Lossless && !t.Stereo
	switch quality {
	case "lossless":
		return lossless
	case "lossy":
		return !lossless
	default:
		return true
	}
}

func sameLanguage(a, b string) bool {
	return strings.EqualFold(a, b) || languageName(a) == languageName(b)
}

// trackDescription says what a chosen track is, as it is on the disc.
func trackDescription(t store.PlannedAudio) string {
	if t.Stereo {
		return "2.0 made by ARFABIT"
	}
	if t.Source != "" {
		// The language is already the outcome's own heading.
		if _, rest, ok := strings.Cut(t.Source, " · "); ok {
			return rest
		}
		return t.Source
	}
	return t.Label
}

// DescribeSoundChoice reads a choice as a short phrase: "pick one of 7.1 or
// 5.1, lossless".
func DescribeSoundChoice(c config.SoundChoice) string {
	var b strings.Builder
	if c.Mode == config.SoundAll {
		b.WriteString("keep all of ")
	} else {
		b.WriteString("pick one of ")
	}

	if len(c.Layouts) == 0 {
		b.WriteString("any layout")
	} else {
		names := make([]string, len(c.Layouts))
		for i, l := range c.Layouts {
			names[i] = l
			if l == "stereo" {
				names[i] = "2.0"
			}
		}
		b.WriteString(strings.Join(names, " or "))
	}

	switch c.Quality {
	case "lossless":
		b.WriteString(", lossless")
	case "lossy":
		b.WriteString(", not lossless")
	}
	return b.String()
}

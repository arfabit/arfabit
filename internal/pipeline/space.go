package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arfabit/arfabit/internal/meta"
)

// Space is the disk check the Plan runs before offering to start.
//
// ARFABIT does not watch disk space in the background and shows no space
// meter. It speaks up at exactly one moment: when the disc in the drive will
// not fit (§8).
type Space struct {
	Needed int64
	Free   int64

	// Originals is how much the originals take, beside their films (§6).
	// Library is the rest of the library: the films.
	Originals int64
	Library   int64

	Fits  bool
	Tight bool

	// Unknown means the check itself did not work. ARFABIT then lets the job
	// go ahead: refusing to start because a check failed would be ARFABIT
	// getting in the way over its own shortcoming, not a real shortage.
	Unknown bool
}

// tightMargin is how close to the limit counts as worth mentioning, since an
// estimate can run low.
const tightMargin = 1.10

// CheckSpace compares what a job needs against what is free in the library,
// where the original and its films are made.
func CheckSpace(needed int64, libraryDir string) (Space, error) {
	free, err := freeBytes(libraryDir)
	if err != nil {
		// Fail open: the job may proceed, and the Plan says the check did not
		// work rather than pretending there is no room.
		return Space{Needed: needed, Fits: true, Unknown: true}, err
	}

	originals, films := librarySizes(libraryDir)
	s := Space{
		Needed:    needed,
		Free:      free,
		Originals: originals,
		Library:   films,
	}
	s.Fits = free >= needed
	s.Tight = s.Fits && float64(free) < float64(needed)*tightMargin

	return s, nil
}

// Describe states the four numbers and nothing else.
//
// The sizes are there because they are almost always the answer: the user
// has originals they no longer need, and this is the moment they would want
// to know it. ARFABIT never offers to remove anything.
func (s Space) Describe() string {
	if s.Unknown {
		return "ARFABIT could not check how much room is left, so it will go ahead. Keep an eye on your free space."
	}
	if s.Fits && !s.Tight {
		return ""
	}

	lead := "There is not enough room for this disc."
	if s.Tight {
		lead = "There is just enough room for this disc, and the estimate could be low."
	}

	return fmt.Sprintf(
		"%s\n\nThis disc needs about %s.\nThe drive has %s free.\nYour originals take %s.\nYour films take %s.",
		lead,
		HumanBytes(s.Needed),
		HumanBytes(s.Free),
		HumanBytes(s.Originals),
		HumanBytes(s.Library),
	)
}

// HumanBytes renders a size the way a person reads it.
func HumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTP"[exp])
}

// librarySizes totals the library in two parts: the originals, with their
// subtitle files, and everything else.
func librarySizes(dir string) (originals, rest int64) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if meta.IsOriginal(d.Name()) {
			originals += info.Size()
		} else {
			rest += info.Size()
		}
		return nil
	})
	return originals, rest
}

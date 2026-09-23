package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
)

// Space is the disk check the Plan runs before offering to start.
//
// ARFABIT does not watch disk space in the background and shows no space
// meter. It speaks up at exactly one moment: when the disc in the drive will
// not fit (§8).
type Space struct {
	Needed    int64
	Free      int64
	Masters   int64
	Library   int64
	Fits      bool
	Tight     bool
	MastersAt string
}

// tightMargin is how close to the limit counts as worth mentioning, since an
// estimate can run low.
const tightMargin = 1.10

// CheckSpace compares what a job needs against what is free.
func CheckSpace(needed int64, mastersDir, libraryDir string) (Space, error) {
	free, err := freeBytes(mastersDir)
	if err != nil {
		return Space{}, err
	}

	s := Space{
		Needed:    needed,
		Free:      free,
		Masters:   dirSize(mastersDir),
		Library:   dirSize(libraryDir),
		MastersAt: mastersDir,
	}
	s.Fits = free >= needed
	s.Tight = s.Fits && float64(free) < float64(needed)*tightMargin

	return s, nil
}

// Describe states the four numbers and nothing else.
//
// The folder sizes are there because they are almost always the answer: the
// user has masters they no longer need, and this is the moment they would want
// to know it. ARFABIT offers to open the folder and never to remove anything.
func (s Space) Describe() string {
	if s.Fits && !s.Tight {
		return ""
	}

	lead := "There is not enough room for this disc."
	if s.Tight {
		lead = "There is just enough room for this disc, and the estimate could be low."
	}

	return fmt.Sprintf(
		"%s\n\nThis rip needs about %s.\nThe drive has %s free.\nYour masters folder holds %s.\nYour library folder holds %s.",
		lead,
		HumanBytes(s.Needed),
		HumanBytes(s.Free),
		HumanBytes(s.Masters),
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

// dirSize totals a directory, returning zero when it cannot be read.
//
// This is only ever shown as information, so an unreadable folder reports zero
// rather than stopping the check.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

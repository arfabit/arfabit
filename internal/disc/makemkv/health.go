package makemkv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
)

// Access is how MakeMKV is talking to the drive, which decides how fast it can
// read.
type Access string

const (
	// AccessLibreDrive is the drive answering directly. Encrypted discs can
	// then be read at whatever speed the drive manages.
	AccessLibreDrive Access = "libredrive"

	// AccessOS is the operating system standing in the way. Encrypted discs
	// are then read at roughly the speed they would play at, because that is
	// all the drive will admit to.
	AccessOS Access = "os"

	AccessUnknown Access = "unknown"
)

// Health is what can be learned about a drive without reading a whole disc.
type Health struct {
	Drive disc.Drive `json:"drive"`

	Access Access `json:"access"`

	// LibreDrive is the version string MakeMKV reports, when it reports one.
	LibreDrive string `json:"libredrive,omitempty"`

	// Messages are everything MakeMKV said, kept whole.
	Messages []Message `json:"messages,omitempty"`
}

// Explain says what the access mode means for how long a disc will take.
//
// This is the difference between a Blu-ray taking forty minutes and taking
// four hours, and nothing else about the setup makes anywhere near that much
// difference.
func (h Health) Explain() string {
	switch h.Access {
	case AccessLibreDrive:
		return fmt.Sprintf(
			"This drive is answering ARFABIT directly (LibreDrive %s), so discs are read as fast as the drive manages.",
			h.LibreDrive)

	case AccessOS:
		return "This drive is being reached through macOS rather than directly, which limits an encrypted disc to about the speed it would play at — a film can take hours rather than tens of minutes. " +
			"Drives vary in whether they support the direct mode, and some need particular firmware. Closing other programs that use the drive is worth trying first."

	default:
		return "ARFABIT could not tell how it is reaching this drive."
	}
}

// Fast reports whether the drive is in the mode that reads quickly.
func (h Health) Fast() bool { return h.Access == AccessLibreDrive }

// CheckHealth asks the drive how it is being reached.
//
// This lists drives, which does not open the disc, so it is quick and does not
// disturb anything.
func (b *Backend) CheckHealth(ctx context.Context) ([]Health, error) {
	b.busy.Lock()
	defer b.busy.Unlock()

	res, err := b.run(ctx, nil, "info", fmt.Sprintf("disc:%d", listDrivesIndex))
	if res == nil {
		return nil, err
	}

	access, version := AccessUnknown, ""
	for _, m := range res.Messages {
		switch {
		case m.Code == msgLibreDrive:
			access = AccessLibreDrive
			version = libreDriveVersion(m.Text)
		case m.Code == msgOSAccessMode && access != AccessLibreDrive:
			access = AccessOS
		}
	}

	health := make([]Health, 0, len(res.Drives))
	for _, d := range res.Drives {
		health = append(health, Health{
			Drive:      d,
			Access:     access,
			LibreDrive: version,
			Messages:   res.Messages,
		})
	}

	return health, nil
}

// libreDriveVersion pulls the version out of "Using LibreDrive mode (v06.3
// id=866A98CB9C4E)".
//
// A version is a nicety, so anything unexpected yields nothing rather than an
// error: the mode itself is what matters.
func libreDriveVersion(text string) string {
	open := strings.Index(text, "(")
	if open < 0 {
		return ""
	}
	inner := text[open+1:]
	if close := strings.Index(inner, ")"); close >= 0 {
		inner = inner[:close]
	}
	if space := strings.Index(inner, " "); space >= 0 {
		inner = inner[:space]
	}
	return strings.TrimSpace(inner)
}

// ReadSpeed is a measured read rate.
type ReadSpeed struct {
	Bytes int64         `json:"bytes"`
	Took  time.Duration `json:"took"`
}

// MBPerSecond is the measured rate.
func (r ReadSpeed) MBPerSecond() float64 {
	if r.Took <= 0 {
		return 0
	}
	return float64(r.Bytes) / 1_000_000 / r.Took.Seconds()
}

// Describe puts the measured speed in terms of how long a film would take.
func (r ReadSpeed) Describe(discSize int64) string {
	rate := r.MBPerSecond()
	if rate <= 0 {
		return "ARFABIT could not measure the drive's speed."
	}

	line := fmt.Sprintf("This drive reads at about %.1f MB per second.", rate)
	if discSize > 0 {
		hours := float64(discSize) / 1_000_000 / rate / 3600
		line += fmt.Sprintf(" A disc this size would take about %s.", roughly(hours))
	}
	return line
}

func roughly(hours float64) string {
	minutes := int(hours * 60)
	switch {
	case minutes < 1:
		return "under a minute"
	case minutes < 90:
		return fmt.Sprintf("%d minutes", minutes)
	default:
		return fmt.Sprintf("%.1f hours", hours)
	}
}

package makemkv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/arfabit/arfabit/internal/disc"
)

// DefaultMinLength is MakeMKV's own default: titles shorter than this are
// hidden. It filters menus, logos and stingers, which is almost always what a
// user wants. Scan with MinLength 0 to see everything (§8).
const DefaultMinLength = 120 * time.Second

// listDrivesIndex is a pseudo-index that makes makemkvcon enumerate drives.
//
// It then tries to open disc 9999, which does not exist, so a trailing
// "failed to open disc" message is expected and must be ignored.
const listDrivesIndex = 9999

// Backend runs makemkvcon.
type Backend struct {
	// Path to makemkvcon. Empty means locate it on first use.
	Path string

	// MinLength hides titles shorter than this. Zero means DefaultMinLength;
	// use ShowAll to see every title.
	MinLength time.Duration

	// ShowAll disables the length filter entirely.
	ShowAll bool

	// Timeout bounds a single makemkvcon run. Zero means no limit: a scan of a
	// scratched disc can legitimately take many minutes as the drive retries.
	Timeout time.Duration

	// OnMessage receives MakeMKV's messages as they arrive, for live logging.
	// Optional.
	OnMessage func(Message)
}

// Name identifies the backend in logs and the UI.
func (b *Backend) Name() string { return "MakeMKV" }

// Drives lists the optical drives MakeMKV can see.
func (b *Backend) Drives() ([]disc.Drive, error) {
	res, err := b.run(context.Background(), "info", fmt.Sprintf("disc:%d", listDrivesIndex))
	if err != nil {
		return nil, err
	}
	// The trailing 5010 belongs to the pseudo-index, not to any real drive, so
	// it is deliberately not treated as a failure here.
	return res.Drives, nil
}

// Scan enumerates the titles on the disc in the given drive.
func (b *Backend) Scan(driveIndex int) (*disc.Disc, error) {
	return b.ScanContext(context.Background(), driveIndex)
}

// ScanContext is Scan with cancellation, so the UI can stop a scan that is
// taking too long on a damaged disc.
func (b *Backend) ScanContext(ctx context.Context, driveIndex int) (*disc.Disc, error) {
	res, err := b.run(ctx, "info", fmt.Sprintf("disc:%d", driveIndex))
	if err != nil {
		return nil, err
	}

	// A scan that produced no titles is a failure the user needs to know
	// about, even though makemkvcon exits zero.
	if len(res.Disc.Titles) == 0 {
		return nil, &Error{
			Op:       "scan",
			Err:      errNoTitles,
			Messages: res.Messages,
		}
	}

	if hasCode(res.Messages, msgOSAccessMode) && b.OnMessage != nil {
		// Not fatal — the scan worked — but it means raw access was
		// unavailable, which usually degrades what MakeMKV can read.
		b.OnMessage(Message{
			Code: msgOSAccessMode,
			Text: "Opened in OS access mode; another program may be using the drive.",
		})
	}

	return res.Disc, nil
}

var errNoTitles = fmt.Errorf("no titles found on disc")

// args builds the argument list common to every invocation.
func (b *Backend) args(extra ...string) []string {
	// -r is robot mode; --cache=1 keeps memory use low, since ARFABIT re-reads
	// nothing from MakeMKV's cache.
	args := []string{"-r", "--cache=1"}

	switch {
	case b.ShowAll:
		args = append(args, "--minlength=0")
	case b.MinLength > 0:
		args = append(args, fmt.Sprintf("--minlength=%d", int(b.MinLength.Seconds())))
	default:
		args = append(args, fmt.Sprintf("--minlength=%d", int(DefaultMinLength.Seconds())))
	}

	return append(args, extra...)
}

// run executes makemkvcon and parses its output.
func (b *Backend) run(ctx context.Context, extra ...string) (*ScanResult, error) {
	path := b.Path
	if path == "" {
		p, err := Locate()
		if err != nil {
			return nil, err
		}
		path = p
	}

	if b.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, b.args(extra...)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// makemkvcon writes robot records to stdout and little to stderr, but
	// anything it does write there belongs in the log verbatim.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, &Error{Op: "start", Err: err}
	}

	res, parseErr := ParseScanFunc(stdout, b.OnMessage)

	// Drain anything left so the child never blocks on a full pipe.
	_, _ = io.Copy(io.Discard, stdout)

	waitErr := cmd.Wait()

	switch {
	case parseErr != nil:
		return nil, &Error{Op: "parse output", Err: parseErr}
	case waitErr != nil:
		msgs := res.Messages
		if s := stderr.String(); s != "" {
			msgs = append(msgs, Message{Text: s})
		}
		return nil, &Error{Op: "run", Err: waitErr, Messages: msgs}
	}

	return res, nil
}

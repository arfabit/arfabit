package makemkv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
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
//
// Only one makemkvcon may touch a drive at a time. Two at once drop the drive
// out of LibreDrive into degraded OS access, and then time out mid-read:
//
//	Optical drive "..." opened in OS access mode.
//	Error 'Scsi error - HARDWARE ERROR:TIMEOUT ON LOGICAL UNIT' ...
//
// So every command is serialised here rather than left to callers to
// co-ordinate, because a caller that forgets corrupts a rip.
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

	// busy serialises access to the drive.
	busy sync.Mutex
}

// TryLock takes the drive if nothing else holds it.
//
// Used by anything that would rather skip a turn than wait, such as the poll
// that watches for a disc being put in.
func (b *Backend) TryLock() bool { return b.busy.TryLock() }

// Unlock releases a drive taken with TryLock.
func (b *Backend) Unlock() { b.busy.Unlock() }

// Name identifies the backend in logs and the UI.
func (b *Backend) Name() string { return "MakeMKV" }

// Drives lists the optical drives MakeMKV can see.
func (b *Backend) Drives() ([]disc.Drive, error) {
	b.busy.Lock()
	defer b.busy.Unlock()

	return b.drivesLocked()
}

// DrivesIfFree lists the drives only when nothing else is using them.
//
// Reports ok=false rather than waiting: a poll that queues behind a rip would
// pile up, and the answer would be stale by the time it arrived.
func (b *Backend) DrivesIfFree() ([]disc.Drive, bool) {
	if !b.busy.TryLock() {
		return nil, false
	}
	defer b.busy.Unlock()

	drives, err := b.drivesLocked()
	if err != nil {
		return nil, true
	}
	return drives, true
}

func (b *Backend) drivesLocked() ([]disc.Drive, error) {
	res, err := b.run(context.Background(), "info", fmt.Sprintf("disc:%d", listDrivesIndex))

	// The pseudo-index lists the drives and then tries to open disc 9999,
	// which does not exist, so makemkvcon always exits non-zero here — 255 in
	// practice. The drives it printed first are still good, and treating that
	// exit as a failure reports "no drive" on a machine that has one.
	if res != nil && len(res.Drives) > 0 {
		return res.Drives, nil
	}
	if err != nil {
		return nil, err
	}
	return res.Drives, nil
}

// Scan enumerates the titles on the disc in the given drive.
func (b *Backend) Scan(driveIndex int) (*disc.Disc, error) {
	return b.ScanContext(context.Background(), driveIndex)
}

// ScanContext is Scan with cancellation, so the UI can stop a scan that is
// taking too long on a damaged disc.
func (b *Backend) ScanContext(ctx context.Context, driveIndex int) (*disc.Disc, error) {
	b.busy.Lock()
	defer b.busy.Unlock()

	res, err := b.run(ctx, "info", fmt.Sprintf("disc:%d", driveIndex))
	if err != nil && (res == nil || len(res.Disc.Titles) == 0) {
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
		// The parsed result is returned alongside the error: makemkvcon exits
		// non-zero in situations where what it already printed is perfectly
		// good, and the caller is better placed to judge than this is.
		return res, &Error{Op: "run", Err: waitErr, Messages: msgs}
	}

	return res, nil
}

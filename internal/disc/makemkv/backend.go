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
// Nothing else in this package should mention it, and nothing outside this
// file should see what it produces beyond the drive list itself. It has caused
// the same class of mistake three times:
//
//   - It tries to open disc 9999, which does not exist, so it always ends with
//     "failed to open disc". Reading that as a real error meant reporting a
//     broken disc when nothing was wrong.
//   - It therefore always exits non-zero — 255 in practice. Treating that as
//     failure meant reporting no drive on a machine that had one.
//   - It prints "opened in OS access mode" while enumerating, which is about
//     the enumeration and not about reading a disc. Reading it as the read
//     mode meant telling people their drive was slow when it was not.
//
// The pattern is the same every time: its output looks like information about
// a disc and is nothing of the sort. So listDrives below returns drives and
// only drives, and the messages are dropped where they cannot be mistaken for
// anything.
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

	// CacheMB is the read cache MakeMKV uses, in megabytes. Zero leaves the
	// choice to MakeMKV, which is the right default.
	//
	// This matters more than anything else about how a disc is read. ARFABIT
	// once passed 1 here, on the guess that a small cache would keep memory
	// use down; reading a 40 GB disc a megabyte at a time is what that
	// actually means, and it held a drive capable of far better to about
	// 3 MB/s.
	CacheMB int

	// OnMessage receives MakeMKV's messages as they arrive, for live logging.
	// Optional, and only a default: callers that want messages from one
	// particular command pass a callback to that command instead, because a
	// shared field cannot be set safely while other commands are running.
	OnMessage func(Message)

	// busy serialises access to the drive.
	busy sync.Mutex

	// lastAccess is the mode the most recent real scan used, which is the
	// only place it can be learned.
	lastAccess        Access
	lastLibreDriveVer string
}

// LastAccess reports the mode the most recent scan used, and whether one has
// happened at all.
func (b *Backend) LastAccess() (Access, string, bool) {
	b.busy.Lock()
	defer b.busy.Unlock()

	return b.lastAccessLocked()
}

// lastAccessLocked is LastAccess for callers already holding the drive.
func (b *Backend) lastAccessLocked() (Access, string, bool) {
	if b.lastAccess == "" {
		return AccessUnknown, "", false
	}
	return b.lastAccess, b.lastLibreDriveVer, true
}

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

// drivesLocked lists the drives and discards everything else.
//
// Dropping the messages is the point, not an oversight: see listDrivesIndex.
// The only thing this command can honestly tell anybody is which drives exist
// and what is in them.
func (b *Backend) drivesLocked() ([]disc.Drive, error) {
	res, err := b.run(context.Background(), nil, "info", fmt.Sprintf("disc:%d", listDrivesIndex))

	// A non-zero exit here is expected, and the drives printed before it are
	// good. Only an empty list is worth reporting as a failure.
	if res != nil && len(res.Drives) > 0 {
		return res.Drives, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, nil
}

// Scan enumerates the titles on the disc in the given drive.
func (b *Backend) Scan(driveIndex int) (*disc.Disc, error) {
	return b.ScanContext(context.Background(), driveIndex)
}

// ScanContext is Scan with cancellation, so the UI can stop a scan that is
// taking too long on a damaged disc.
func (b *Backend) ScanContext(ctx context.Context, driveIndex int) (*disc.Disc, error) {
	return b.ScanWithMessages(ctx, driveIndex, b.OnMessage)
}

// ScanWithMessages is ScanContext with somewhere to send MakeMKV's running
// commentary.
//
// The callback is passed in rather than set on the Backend: a scan shares the
// Backend with the poll that watches the drive, and a field cannot be assigned
// safely while another command is using it.
func (b *Backend) ScanWithMessages(ctx context.Context, driveIndex int, onMessage func(Message)) (*disc.Disc, error) {
	b.busy.Lock()
	defer b.busy.Unlock()

	res, err := b.run(ctx, onMessage, "info", fmt.Sprintf("disc:%d", driveIndex))
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

	// A real scan is the only thing that reveals how the drive is being
	// reached, so the answer is kept for the drive report to use later.
	access, version := AccessFrom(res.Messages)
	if access != AccessUnknown {
		b.lastAccess, b.lastLibreDriveVer = access, version
	}

	// MakeMKV says it opened the drive in OS access mode every time, before
	// it switches to LibreDrive, so the message alone means nothing. Only a
	// scan that never switched was really read through the system. Not fatal
	// — the scan worked — but it is slow, and worth saying. The cause is not
	// known, so none is given.
	if access == AccessOS && onMessage != nil {
		onMessage(Message{
			Code: msgOSAccessMode,
			Text: "This disc was read through your computer's system rather than directly, which is slower.",
		})
	}

	return res.Disc, nil
}

var errNoTitles = fmt.Errorf("no titles found on disc")

// args builds the argument list common to every invocation.
func (b *Backend) args(extra ...string) []string {
	args := []string{"-r"}
	if b.CacheMB > 0 {
		args = append(args, fmt.Sprintf("--cache=%d", b.CacheMB))
	}

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
//
// onMessage may be nil, and is per-call rather than taken from the Backend so
// that concurrent commands cannot tread on each other.
func (b *Backend) run(ctx context.Context, onMessage func(Message), extra ...string) (*ScanResult, error) {
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

	res, parseErr := ParseScanFunc(stdout, onMessage)

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

package makemkv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RipRequest describes one rip.
type RipRequest struct {
	// DriveIndex is the drive to read.
	DriveIndex int

	// Titles are the title indexes to rip. Empty means every title MakeMKV
	// offers, which is rarely what anyone wants — the Plan normally names
	// exactly one.
	Titles []int

	// OutputDir receives the .mkv files. Created if missing.
	OutputDir string

	// OnProgress is called as the rip advances. Optional; must not block.
	OnProgress func(Progress)

	// OnMessage receives MakeMKV's messages as they arrive. Optional.
	OnMessage func(Message)
}

// RipResult is what a completed rip produced.
type RipResult struct {
	// Files are the .mkv files written, in the order MakeMKV reported them.
	Files []string

	// Messages are everything MakeMKV said, kept for the job log.
	Messages []Message
}

// Rip reads titles off the disc and writes them to OutputDir.
//
// This is the long one: a Blu-ray feature takes 20-40 minutes depending on the
// drive. Progress arrives through OnProgress throughout, and ctx cancellation
// stops the rip.
//
// makemkvcon accepts a single title index or the literal "all" — there is no
// syntax for an arbitrary subset — so ripping several named titles runs it once
// per title.
func (b *Backend) Rip(ctx context.Context, req RipRequest) (*RipResult, error) {
	if req.OutputDir == "" {
		return nil, fmt.Errorf("rip: no output directory")
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return nil, &Error{Op: "prepare output directory", Err: err}
	}

	if len(req.Titles) > 1 {
		combined := &RipResult{}
		for _, title := range req.Titles {
			one := req
			one.Titles = []int{title}
			res, err := b.ripOnce(ctx, one)
			if err != nil {
				return nil, err
			}
			combined.Files = append(combined.Files, res.Files...)
			combined.Messages = append(combined.Messages, res.Messages...)
		}
		return combined, nil
	}

	return b.ripOnce(ctx, req)
}

// ripOnce runs makemkvcon a single time.
func (b *Backend) ripOnce(ctx context.Context, req RipRequest) (*RipResult, error) {

	path := b.Path
	if path == "" {
		p, err := Locate()
		if err != nil {
			return nil, err
		}
		path = p
	}

	// Snapshot the directory so the files this rip produced can be identified
	// without trusting MakeMKV's prose.
	before, err := mkvFiles(req.OutputDir)
	if err != nil {
		return nil, &Error{Op: "read output directory", Err: err}
	}

	cmd := exec.CommandContext(ctx, path, b.ripArgs(req)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, &Error{Op: "start rip", Err: err}
	}

	res, scanErr := b.readRipOutput(stdout, req)

	// Drain so the child never blocks writing to a full pipe.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	switch {
	case scanErr != nil:
		return nil, &Error{Op: "read rip output", Err: scanErr, Messages: res.Messages}
	case waitErr != nil:
		msgs := res.Messages
		if s := stderr.String(); s != "" {
			msgs = append(msgs, Message{Text: s})
		}
		return nil, &Error{Op: "rip", Err: waitErr, Messages: msgs}
	}

	after, err := mkvFiles(req.OutputDir)
	if err != nil {
		return nil, &Error{Op: "read output directory", Err: err, Messages: res.Messages}
	}
	res.Files = newFiles(req.OutputDir, before, after)

	// makemkvcon exits zero even when it saved nothing, so verify rather than
	// trust the exit code.
	if len(res.Files) == 0 {
		return nil, &Error{Op: "rip", Err: errNoFilesWritten, Messages: res.Messages}
	}

	return res, nil
}

var errNoFilesWritten = fmt.Errorf("no files were written")

// ripArgs builds the makemkvcon command line for a rip.
func (b *Backend) ripArgs(req RipRequest) []string {
	// --progress=-same puts progress records on stdout alongside everything
	// else, so one stream carries the whole job.
	args := []string{"-r", "--cache=1", "--progress=-same"}

	switch {
	case b.ShowAll:
		args = append(args, "--minlength=0")
	case b.MinLength > 0:
		args = append(args, fmt.Sprintf("--minlength=%d", int(b.MinLength.Seconds())))
	default:
		args = append(args, fmt.Sprintf("--minlength=%d", int(DefaultMinLength.Seconds())))
	}

	return append(args,
		"mkv",
		fmt.Sprintf("disc:%d", req.DriveIndex),
		titleSelector(req.Titles),
		req.OutputDir,
	)
}

// titleSelector renders the title argument makemkvcon expects.
//
// makemkvcon takes a single title index or the literal "all"; it has no syntax
// for an arbitrary subset, so Rip calls it once per title when several are
// named.
func titleSelector(titles []int) string {
	if len(titles) == 1 {
		return strconv.Itoa(titles[0])
	}
	return "all"
}

// readRipOutput consumes makemkvcon's output, reporting progress and
// collecting the files it saved.
func (b *Backend) readRipOutput(r io.Reader, req RipRequest) (*RipResult, error) {
	res := &RipResult{}
	var tracker progressTracker

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		rec, ok := ParseLine(sc.Text())
		if !ok {
			continue
		}

		if p, ok := tracker.apply(rec); ok {
			if req.OnProgress != nil {
				req.OnProgress(p)
			}
			continue
		}

		if rec.Type != recMSG {
			continue
		}

		code, err := rec.intField(0)
		if err != nil || noisyMessages[code] {
			continue
		}

		m := Message{Code: code, Text: rec.field(3)}
		res.Messages = append(res.Messages, m)
		if req.OnMessage != nil {
			req.OnMessage(m)
		}
	}

	return res, sc.Err()
}

// mkvFiles lists the .mkv files in a directory.
//
// The output files are found by comparing the directory before and after the
// rip rather than by parsing MakeMKV's messages. MakeMKV reports saved files
// only inside human-readable text, and reading English prose to learn what was
// written is exactly what §15 warns against — the directory is the fact.
func mkvFiles(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mkv") {
			found[e.Name()] = true
		}
	}
	return found, nil
}

// newFiles returns the entries in after that were not in before, sorted.
func newFiles(dir string, before, after map[string]bool) []string {
	var out []string
	for name := range after {
		if !before[name] {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out
}

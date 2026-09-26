package subs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/arfabit/arfabit/internal/ffmpeg"
)

// ReadTrack pulls one subtitle track out of a master, as it is, and decodes
// its pictures. stream is the track's index in the master. Nothing is written
// to disk.
func ReadTrack(ctx context.Context, master string, stream int) ([]Subtitle, error) {
	bin, err := ffmpeg.Locate()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error", "-i", master,
		"-map", fmt.Sprintf("0:%d", stream), "-c", "copy", "-f", "sup", "pipe:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	subtitles, parseErr := ParseSUP(out)

	// Whatever the reader left unread is drained, so ffmpeg can always
	// finish. Otherwise a reader that stopped early would leave ffmpeg
	// blocked writing to a full pipe, and this waiting on it for good.
	_, _ = io.Copy(io.Discard, out)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("%v\n%s", waitErr, stderr.String())
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if len(subtitles) == 0 {
		return nil, errors.New("the track holds no subtitles ARFABIT can read; only picture subtitles from Blu-ray (PGS) are read today")
	}
	return subtitles, nil
}

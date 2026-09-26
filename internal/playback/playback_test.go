package playback

import (
	"strings"
	"testing"
)

// Only what was tested is claimed, and the one lossless format that does not
// play directly gets a suggestion of the same width.
func TestNotes(t *testing.T) {
	if n := Note(PlexAppleTV, "audio", "dts", 8); n != "" {
		t.Errorf("DTS-HD MA played directly, but has a note: %q", n)
	}
	if n := Note(PlexAppleTV, "audio", "pcm_s24le", 6); n != "" {
		t.Errorf("PCM played directly, but has a note: %q", n)
	}
	if n := Note(PlexAppleTV, "audio", "truehd", 8); !strings.Contains(n, "FLAC 7.1") {
		t.Errorf("TrueHD 7.1 note = %q, want a FLAC 7.1 suggestion", n)
	}
	if n := Note(PlexAppleTV, "subtitle", "hdmv_pgs_subtitle", 0); !strings.Contains(n, "whole video") {
		t.Errorf("PGS note = %q", n)
	}
	if n := Note(PlexAppleTV, "video", "vc1", 0); !strings.Contains(n, "Not tested") {
		t.Errorf("VC-1 was never tried, but the note says %q", n)
	}
	if Check("Roku", "audio", "aac") != Untested {
		t.Error("a device never tested was assumed to play something")
	}
}

package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// MakeMKV says it opened the drive in OS access mode on every scan, before it
// switches to LibreDrive. Saying the disc was read slowly on the strength of
// that message alone contradicted the very next line of the log.
func TestOSAccessIsOnlyMentionedWhenItStuck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for makemkvcon is a shell script")
	}

	// Disc scans are kept locally, not committed (see .gitignore).
	recorded, err := os.ReadFile("testdata/scan-bd-1.txt")
	if err != nil {
		t.Skipf("fixture scan-bd-1.txt not present; see docs/ARCHITECTURE.md Appendix A")
	}
	var withoutLibreDrive []string
	for _, line := range strings.Split(string(recorded), "\n") {
		if !strings.HasPrefix(line, "MSG:1011,") {
			withoutLibreDrive = append(withoutLibreDrive, line)
		}
	}

	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{"switched to LibreDrive", string(recorded), false},
		{"stayed in OS access mode", strings.Join(withoutLibreDrive, "\n"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "scan.txt"), []byte(tc.output), 0o644); err != nil {
				t.Fatal(err)
			}
			fake := filepath.Join(dir, "makemkvcon")
			script := "#!/bin/sh\ncat '" + filepath.Join(dir, "scan.txt") + "'\n"
			if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			var said bool
			b := &Backend{Path: fake}
			if _, err := b.ScanWithMessages(context.Background(), 0, func(m Message) {
				if strings.Contains(m.Text, "rather than directly") {
					said = true
				}
			}); err != nil {
				t.Fatal(err)
			}
			if said != tc.want {
				t.Errorf("said it was read through the system: %v, want %v", said, tc.want)
			}
		})
	}
}

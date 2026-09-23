package autostart

import (
	"strings"
	"testing"
)

// KeepAlive relaunches the program the moment it exits, so Stop appears not to
// work and neither does kill: it comes straight back and there is no way to
// tell why. Starting at login is what was asked for; refusing to stay stopped
// is not.
//
// The template is per-platform, so this checks whichever one is compiled in.
func TestStartupEntryDoesNotRelaunch(t *testing.T) {
	entry := startupTemplate()
	if entry == "" {
		t.Skip("no startup entry on this platform")
	}

	for _, relaunch := range []string{"KeepAlive", "Restart="} {
		if strings.Contains(entry, relaunch) {
			t.Errorf("the startup entry contains %q, so ARFABIT would come back after being stopped:\n%s",
				relaunch, entry)
		}
	}
}

// The whole point of the entry is to start at login.
func TestStartupEntryStartsAtLogin(t *testing.T) {
	entry := startupTemplate()
	if entry == "" {
		t.Skip("no startup entry on this platform")
	}

	if !strings.Contains(entry, "RunAtLoad") && !strings.Contains(entry, "WantedBy") {
		t.Errorf("the startup entry does not start at login:\n%s", entry)
	}
}

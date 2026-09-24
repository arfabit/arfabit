package makemkv

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The difference between the two access modes is the difference between a
// Blu-ray taking forty minutes and taking four hours.
func TestAccessModeIsExplainedInTermsOfTime(t *testing.T) {
	slow := Health{Access: AccessOS}
	if slow.Fast() {
		t.Error("OS access was reported as fast")
	}
	explanation := slow.Explain()
	if !strings.Contains(explanation, "hours") {
		t.Errorf("the explanation does not say what it costs: %q", explanation)
	}
	if !strings.Contains(explanation, "holding the disc open") {
		t.Errorf("the explanation does not name the usual cause: %q", explanation)
	}

	fast := Health{Access: AccessLibreDrive, LibreDrive: "v06.3"}
	if !fast.Fast() {
		t.Error("LibreDrive was not reported as fast")
	}
	if !strings.Contains(fast.Explain(), "v06.3") {
		t.Errorf("the explanation does not name the version: %q", fast.Explain())
	}
}

// Unknown is its own answer, not a guess in either direction.
func TestUnknownAccessSaysSo(t *testing.T) {
	h := Health{Access: AccessUnknown}
	if h.Fast() {
		t.Error("an unknown access mode was treated as fast")
	}
	// Not knowing is its own answer, and it says why: the mode is only
	// reported once a disc has actually been read.
	if !strings.Contains(h.Explain(), "once a disc has been read") {
		t.Errorf("an unknown mode was described as something: %q", h.Explain())
	}
}

func TestLibreDriveVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Using LibreDrive mode (v06.3 id=866A98CB9C4E)", "v06.3"},
		{"Using LibreDrive mode (v06.3)", "v06.3"},
		{"Using LibreDrive mode", ""},
	}
	for _, tc := range tests {
		if got := libreDriveVersion(tc.in); got != tc.want {
			t.Errorf("libreDriveVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A measured speed is only meaningful as how long a disc would take.
func TestReadSpeedDescribesTheWait(t *testing.T) {
	// The rate the slow mode produced in practice.
	slow := ReadSpeed{Bytes: 260_000_000, Took: 100 * time.Second}
	if got := slow.MBPerSecond(); got < 2.5 || got > 2.7 {
		t.Errorf("MBPerSecond = %.2f, want about 2.6", got)
	}

	description := slow.Describe(40_700_000_000)
	if !strings.Contains(description, "hours") {
		t.Errorf("a four-hour disc was not described in hours: %q", description)
	}

	fast := ReadSpeed{Bytes: 1_500_000_000, Took: 100 * time.Second}
	if got := fast.Describe(40_700_000_000); !strings.Contains(got, "minutes") {
		t.Errorf("a fast drive was not described in minutes: %q", got)
	}
}

func TestReadSpeedWithNoMeasurement(t *testing.T) {
	if got := (ReadSpeed{}).Describe(1000); !strings.Contains(got, "could not measure") {
		t.Errorf("an unmeasured speed claimed something: %q", got)
	}
}

// Listing drives does not open a disc, and MakeMKV names the mode only when it
// has. Reading the enumeration's "OS access mode" line as the read mode said
// "slow" even when reading was fast.
func TestAccessComesFromARealScan(t *testing.T) {
	enumeration := []Message{
		{Code: msgVersion, Text: "MakeMKV started"},
		{Code: msgOSAccessMode, Text: `Optical drive "BD-RE" opened in OS access mode.`},
	}
	realScan := append(append([]Message{}, enumeration...),
		Message{Code: msgLibreDrive, Text: "Using LibreDrive mode (v06.3 id=866A98CB9C4E)"})

	// Both lines appear in a fast scan; LibreDrive is the one that counts.
	if access, version := AccessFrom(realScan); access != AccessLibreDrive || version != "v06.3" {
		t.Errorf("a LibreDrive scan read as %q %q", access, version)
	}

	// Enumeration alone genuinely says OS mode, which is why it must not be
	// mistaken for the read mode.
	if access, _ := AccessFrom(enumeration); access != AccessOS {
		t.Errorf("enumeration read as %q", access)
	}

	if access, _ := AccessFrom(nil); access != AccessUnknown {
		t.Errorf("nothing at all read as %q, want unknown", access)
	}
}

// Nothing but this file should know the enumeration index exists, and nothing
// should be able to read what it says.
//
// The same class of mistake has been made three times — its trailing error
// read as a real one, its exit code read as failure, its access-mode line read
// as the read mode — so the guard is structural rather than another comment.
func TestEnumerationSaysNothingAboutReading(t *testing.T) {
	b := &Backend{}

	health, _ := b.CheckHealth(context.Background())
	for _, h := range health {
		if len(h.Messages) != 0 {
			t.Errorf("the drive report carries %d messages from enumeration, which say nothing about reading",
				len(h.Messages))
		}
	}

	// With no scan yet, the mode is unknown rather than guessed either way.
	if access, _, seen := b.LastAccess(); seen || access != AccessUnknown {
		t.Errorf("a mode was reported before any disc was read: %q", access)
	}
}

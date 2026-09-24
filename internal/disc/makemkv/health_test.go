package makemkv

import (
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
	if !strings.Contains(explanation, "Closing other programs") {
		t.Errorf("the explanation suggests nothing to try: %q", explanation)
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
	if !strings.Contains(h.Explain(), "could not tell") {
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

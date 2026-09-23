package makemkv

import (
	"errors"
	"fmt"
)

// ErrNotInstalled means makemkvcon could not be found on PATH or in any of the
// usual install locations.
var ErrNotInstalled = errors.New("makemkvcon not found")

// Error is a failure that carries MakeMKV's own messages alongside it.
//
// The raw messages are always preserved. Per §15, ARFABIT shows a
// plain-language line only when a code is recognised, and shows the underlying
// output either way — never a guess in place of the real thing.
type Error struct {
	Op       string    // "scan", "list drives"
	Err      error     // underlying cause, may be nil
	Messages []Message // what MakeMKV reported
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("makemkv %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("makemkv %s failed", e.Op)
}

func (e *Error) Unwrap() error { return e.Err }

// Explain returns a plain-language description when one of the messages is
// recognised, and ok=false when none is.
//
// A caller that gets ok=false must show the raw messages rather than inventing
// a cause. The list stays short on purpose: every entry is a signature that has
// been seen and understood.
func (e *Error) Explain() (string, bool) {
	for _, m := range e.Messages {
		switch m.Code {
		case msgFailedToOpen:
			return "The disc could not be read. It may be dirty, scratched, or still spinning up.", true
		case msgOSAccessMode:
			return "Another program is using the disc drive. Close MakeMKV if it is open.", true
		}
	}
	return "", false
}

// hasCode reports whether the scan produced a given message code.
func hasCode(msgs []Message, code int) bool {
	for _, m := range msgs {
		if m.Code == code {
			return true
		}
	}
	return false
}

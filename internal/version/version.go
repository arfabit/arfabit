// Package version says which ARFABIT this is.
//
// A release sets it when it is built (docs/RELEASE.md §7):
//
//	go build -ldflags "-X github.com/arfabit/arfabit/internal/version.Version=v0.1.0" ./cmd/arfabit
//
// Anything built without that, from a checkout, is "dev".
package version

// Version is a semantic version such as "v0.1.0", or "dev".
var Version = "dev"

// Dev reports whether this is a development build rather than a release.
//
// A development build keeps the habits that suit someone working on ARFABIT:
// a copy started a second time takes over from the first (docs/RELEASE.md §3).
func Dev() bool {
	return Version == "dev"
}

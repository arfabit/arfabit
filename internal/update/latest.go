// Package update finds, fetches, checks and stages new versions of ARFABIT
// (docs/RELEASE.md §6). It never installs them: ARFABIT exits with 75 when
// nothing is running, and its app installs what is staged.
package update

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LatestURL is where every copy asks for the newest version: a file
// attached to each release on GitHub. GitHub sends this address to the
// newest release's copy.
const LatestURL = "https://github.com/arfabit/arfabit/releases/latest/download/latest.json"

// The public half of the key latest.json is signed with, made once by
// packaging/keys/updatekey. A copy accepts only what this key signed.
//
//go:embed public.key
var publicKeyText string

// PublicKey is the key built into this copy.
func PublicKey() ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyText))
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("internal/update/public.key is not an Ed25519 public key")
	}
	return key
}

// Release is what latest.json says about the newest version.
type Release struct {
	Version string    `json:"version"`
	Date    time.Time `json:"date"`

	// Notes is what changed, in words for the person, shown as written.
	Notes string `json:"notes"`

	// Downloads are by runtime.GOOS: "darwin" for the Mac app as a .zip,
	// "windows" for the installer.
	Downloads map[string]Download `json:"downloads"`
}

// Download is one platform's file and what it must match.
type Download struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// envelope is latest.json itself: the release as JSON, and a signature over
// exactly those bytes. Signing the bytes rather than the parsed release means
// nothing has to be written out the same way twice to be checked.
type envelope struct {
	Signed    string `json:"signed"`
	Signature string `json:"signature"`
}

// Sign makes latest.json from a release, with the private key's seed.
func Sign(r Release, seed []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("the update key must be a %d byte Ed25519 seed, not %d bytes", ed25519.SeedSize, len(seed))
	}
	if _, err := parseVersion(r.Version); err != nil {
		return nil, err
	}

	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), body)

	return json.MarshalIndent(envelope{
		Signed:    base64.StdEncoding.EncodeToString(body),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}, "", "  ")
}

// Verify reads latest.json, and returns the release only if key signed it.
func Verify(data []byte, key ed25519.PublicKey) (Release, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Release{}, fmt.Errorf("latest.json could not be read: %w", err)
	}
	body, err := base64.StdEncoding.DecodeString(env.Signed)
	if err != nil {
		return Release{}, fmt.Errorf("latest.json could not be read: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return Release{}, fmt.Errorf("latest.json could not be read: %w", err)
	}
	if !ed25519.Verify(key, body, sig) {
		return Release{}, errors.New("latest.json is not signed by ARFABIT's update key")
	}

	var r Release
	if err := json.Unmarshal(body, &r); err != nil {
		return Release{}, fmt.Errorf("latest.json could not be read: %w", err)
	}
	if _, err := parseVersion(r.Version); err != nil {
		return Release{}, err
	}
	return r, nil
}

// Newer reports whether version a is later than b. Both are semantic
// versions such as v0.2.0; "dev" is never newer, and never older.
func Newer(a, b string) bool {
	va, errA := parseVersion(a)
	vb, errB := parseVersion(b)
	if errA != nil || errB != nil {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// parseVersion reads vMAJOR.MINOR.PATCH. Releases have nothing after it.
func parseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, fmt.Errorf("%q is not a version such as v0.1.0", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("%q is not a version such as v0.1.0", v)
		}
		out[i] = n
	}
	return out, nil
}

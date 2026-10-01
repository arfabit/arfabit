// Command updatekey makes the Ed25519 key that signs latest.json, the file
// ARFABIT reads to learn of a new version (docs/RELEASE.md §6). Run once:
//
//	go run ./packaging/keys/updatekey
//
// It writes two files:
//
//   - the public half to internal/update/public.key, which is committed and
//     built into ARFABIT, so it can check what it downloads;
//   - the private half to ~/arfabit-keys/update-private.key, which is never
//     committed. It goes into the GitHub secret ARFABIT_UPDATE_KEY, and a copy
//     belongs somewhere safe besides this Mac.
//
// A copy of ARFABIT only accepts updates signed with the key it was built
// with, so this is made once, and the private half must not be lost.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}

	var (
		private = flag.String("private", filepath.Join(home, "arfabit-keys", "update-private.key"), "where to write the private half")
		public  = flag.String("public", "internal/update/public.key", "where to write the public half")
	)
	flag.Parse()

	// An existing key is never replaced: copies already installed accept
	// only updates signed with the key they were built with.
	for _, path := range []string{*private, *public} {
		if _, err := os.Stat(path); err == nil {
			fail(fmt.Errorf("%s already exists, so there is already an update key. Nothing was changed.", path))
		} else if !errors.Is(err, os.ErrNotExist) {
			fail(err)
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail(err)
	}

	if err := os.MkdirAll(filepath.Dir(*private), 0o700); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*private, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(*public), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*public, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		fail(err)
	}

	fmt.Printf("Made the update key.\n\n")
	fmt.Printf("  Public half, to commit:     %s\n", *public)
	fmt.Printf("  Private half, never commit: %s\n\n", *private)
	fmt.Printf("Put the private half into GitHub:\n\n")
	fmt.Printf("  gh secret set ARFABIT_UPDATE_KEY --repo arfabit/arfabit < %s\n\n", *private)
	fmt.Printf("Then keep a copy of it somewhere other than this Mac, such as a password manager.\n")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "%v\n", err)
	os.Exit(1)
}

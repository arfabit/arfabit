// Command latest writes latest.json for a release and signs it with the
// update key (docs/RELEASE.md §6). The release workflow runs it once every
// download is built:
//
//	ARFABIT_UPDATE_KEY=... go run ./packaging/release/latest \
//	  -version v0.2.0 -notes notes.txt \
//	  -darwin dist/ARFABIT-macOS.zip -windows dist/ARFABIT-setup.exe \
//	  -out dist/latest.json
//
// The key is the private half's seed, as written by packaging/keys/updatekey,
// read from the environment so it never appears in a command line or a log.
// Each download's address is where the release will keep it on GitHub.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/update"
)

func main() {
	var (
		ver     = flag.String("version", "", "the release's version, such as v0.2.0")
		notes   = flag.String("notes", "", "a file of what changed, in words for the person")
		darwin  = flag.String("darwin", "", "the Mac app, as a .zip")
		windows = flag.String("windows", "", "the Windows installer")
		base    = flag.String("base", "https://github.com/arfabit/arfabit/releases/download/", "where releases keep their files; the version is added")
		out     = flag.String("out", "latest.json", "where to write latest.json")
	)
	flag.Parse()

	if err := run(*ver, *notes, *darwin, *windows, *base, *out); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(ver, notesPath, darwin, windows, base, out string) error {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ARFABIT_UPDATE_KEY")))
	if err != nil || len(seed) == 0 {
		return errors.New("ARFABIT_UPDATE_KEY is not set to the update key's private half")
	}

	release := update.Release{
		Version:   ver,
		Date:      time.Now().UTC().Truncate(time.Second),
		Downloads: map[string]update.Download{},
	}
	if notesPath != "" {
		text, err := os.ReadFile(notesPath)
		if err != nil {
			return err
		}
		release.Notes = strings.TrimSpace(string(text))
	}

	for platform, path := range map[string]string{"darwin": darwin, "windows": windows} {
		if path == "" {
			continue
		}
		d, err := describe(path, strings.TrimSuffix(base, "/")+"/"+ver+"/"+filepath.Base(path))
		if err != nil {
			return err
		}
		release.Downloads[platform] = d
	}
	if len(release.Downloads) == 0 {
		return errors.New("give at least one download, with -darwin or -windows")
	}

	signed, err := update.Sign(release, seed)
	if err != nil {
		return err
	}

	// A file that does not check out against the key built into ARFABIT
	// would be refused by every copy, so it is not written.
	if _, err := update.Verify(signed, update.PublicKey()); err != nil {
		return fmt.Errorf("latest.json does not verify against internal/update/public.key, so ARFABIT_UPDATE_KEY is not its private half: %w", err)
	}

	return os.WriteFile(out, append(signed, '\n'), 0o644)
}

// describe is a download's address, size and SHA-256.
func describe(path, url string) (update.Download, error) {
	f, err := os.Open(path)
	if err != nil {
		return update.Download{}, err
	}
	defer f.Close()

	hash := sha256.New()
	n, err := io.Copy(hash, f)
	if err != nil {
		return update.Download{}, err
	}
	return update.Download{URL: url, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: n}, nil
}

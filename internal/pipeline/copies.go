package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/store"
)

// A film's SRT is a copy of the one beside its Original, which is where
// subtitles are fixed (§10). Once that one is fixed the copy is out of date.
// ARFABIT records what it wrote into each copy, and replaces a copy only while
// it is still exactly that: a copy anybody else has changed is left alone.

// How a copy stands against the SRT it was taken from.
const (
	CopyCurrent  = "current"   // the same as the Original's SRT
	CopyBehind   = "behind"    // as ARFABIT wrote it, and the Original's has changed since
	CopyChanged  = "changed"   // changed by somebody else since ARFABIT wrote it
	CopyGone     = "gone"      // no longer where ARFABIT put it
	CopyNoOrigin = "no_origin" // the Original's SRT is no longer there
)

// hashOf is the SHA-256 of some bytes, as ARFABIT records it.
func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Hashes hashes files, remembering each until it changes size or time, so a
// page asking many times a second does not read every SRT each time.
type Hashes struct {
	mu   sync.Mutex
	seen map[string]hashed
}

type hashed struct {
	size int64
	mod  time.Time
	sum  string
}

// Of returns a file's SHA-256, or an error if it cannot be read.
func (h *Hashes) Of(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if h != nil {
		h.mu.Lock()
		was, ok := h.seen[path]
		h.mu.Unlock()
		if ok && was.size == info.Size() && was.mod.Equal(info.ModTime()) {
			return was.sum, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := hashOf(data)
	if h != nil {
		h.mu.Lock()
		if h.seen == nil {
			h.seen = map[string]hashed{}
		}
		h.seen[path] = hashed{size: info.Size(), mod: info.ModTime(), sum: sum}
		h.mu.Unlock()
	}
	return sum, nil
}

// CopyState says how a copy stands: one of the Copy... values.
func CopyState(h *Hashes, c store.Copy) string {
	now, err := h.Of(c.To)
	if err != nil {
		return CopyGone
	}
	if now != c.SHA256 {
		return CopyChanged
	}
	origin, err := h.Of(c.From)
	if err != nil {
		return CopyNoOrigin
	}
	if origin != c.SHA256 {
		return CopyBehind
	}
	return CopyCurrent
}

// ErrCopyChanged is a copy somebody else has changed, which ARFABIT leaves
// as it is.
var ErrCopyChanged = errors.New("somebody else has changed this copy since ARFABIT made it, so ARFABIT leaves it as it is")

// BringUpToDate replaces a copy with the SRT beside its Original, as it is
// now, and records what it wrote. It does so only if the copy is still exactly
// what ARFABIT last wrote there, hashed again now: that is the only proof that
// nobody else has changed it. It is written aside and renamed into place, so
// it is never seen half written.
func BringUpToDate(c *store.Copy) error {
	now, err := os.ReadFile(c.To)
	if err != nil {
		return err
	}
	if hashOf(now) != c.SHA256 {
		return ErrCopyChanged
	}
	data, err := os.ReadFile(c.From)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.To), ".arfabit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	// Hashed once more at the last moment, so a change made while this
	// was being written is not lost.
	if again, err := os.ReadFile(c.To); err != nil || hashOf(again) != c.SHA256 {
		os.Remove(name)
		if err != nil {
			return err
		}
		return ErrCopyChanged
	}
	if err := os.Rename(name, c.To); err != nil {
		os.Remove(name)
		return err
	}
	c.SHA256 = hashOf(data)
	return nil
}

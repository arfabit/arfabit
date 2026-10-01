package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arfabit/arfabit/internal/store"
)

// The staging folder holds a few files, each read by ARFABIT, its app, or
// both (docs/RELEASE.md §6). The app looks for them by these names, in the
// same folder: <ARFABIT's own folder>/update.
const (
	// StagedFile names the verified download waiting to be installed,
	// written here.
	StagedFile = "staged.json"

	// FailedFile says a version the app tried to install did not start, and
	// is written by the app, which then put the old version back.
	FailedFile = "failed.json"

	// checkedFile remembers when ARFABIT last asked, so a restart does not
	// ask again the same day.
	checkedFile = "checked.json"
)

// Staged is a new version downloaded, checked, and waiting to be installed.
type Staged struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`

	// File is the download's name, in the staging folder: the Mac app as a
	// .zip, or the Windows installer.
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// Failed is a version the app installed that did not start.
type Failed struct {
	Version string `json:"version"`

	// Detail is what happened, as the app saw it, verbatim.
	Detail string `json:"detail"`
}

// Status is everything the page and the app's menu say about updates.
type Status struct {
	Current   string    `json:"current"`
	CheckedAt time.Time `json:"checked_at,omitempty"`

	// Latest is the newest version found at the last check.
	Latest string `json:"latest,omitempty"`

	// Working says what is happening now, if anything.
	Working string `json:"working,omitempty"`

	Ready  *Staged `json:"ready,omitempty"`
	Failed *Failed `json:"failed,omitempty"`

	// Problem is the last check or download that did not finish, with what
	// was reported, verbatim, in Detail.
	Problem string `json:"problem,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Updater checks for, downloads, checks, and stages new versions.
type Updater struct {
	// Dir is the staging folder, on this computer.
	Dir string

	Current  string // this copy's version
	Platform string // runtime.GOOS
	URL      string // where latest.json is
	Key      ed25519.PublicKey
	Client   *http.Client

	// OnChange is told every new status.
	OnChange func(Status)

	mu        sync.Mutex
	status    Status
	busy      bool
	tried     time.Time // last check started, finished or not
	restarted bool
}

// Load reads what is already staged. A staged version no newer than this
// copy has been installed, and its download is put away.
func (u *Updater) Load() {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.status.Current = u.Current

	var checked struct {
		At     time.Time `json:"at"`
		Latest string    `json:"latest"`
	}
	if readJSON(filepath.Join(u.Dir, checkedFile), &checked) == nil {
		u.status.CheckedAt, u.status.Latest = checked.At, checked.Latest
	}

	var staged Staged
	if readJSON(filepath.Join(u.Dir, StagedFile), &staged) == nil {
		if Newer(staged.Version, u.Current) {
			u.status.Ready = &staged
		} else {
			u.discard(staged)
		}
	}

	var failed Failed
	if readJSON(filepath.Join(u.Dir, FailedFile), &failed) == nil && Newer(failed.Version, u.Current) {
		u.status.Failed = &failed
	}
}

// Status is the current status.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

// Retry forgets that the staged version did not start before, so Update
// and restart tries it again.
func (u *Updater) Retry() {
	u.mu.Lock()
	failed := u.status.Failed
	if failed != nil && u.status.Ready != nil && failed.Version == u.status.Ready.Version {
		u.status.Failed = nil
		_ = os.Remove(filepath.Join(u.Dir, FailedFile))
	}
	u.mu.Unlock()
	u.changed()
}

// Check asks for the newest version, and downloads and stages it if it is
// newer than this copy. A check already under way is not started twice.
func (u *Updater) Check(ctx context.Context) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return
	}
	u.busy = true
	u.tried = time.Now()
	u.status.Working = "Checking for a new version"
	u.status.Problem, u.status.Detail = "", ""
	u.mu.Unlock()
	u.changed()

	problem, err := u.check(ctx)

	u.mu.Lock()
	u.busy = false
	u.status.Working = ""
	if err != nil {
		u.status.Problem, u.status.Detail = problem, err.Error()
	}
	u.mu.Unlock()
	u.changed()
}

func (u *Updater) check(ctx context.Context) (string, error) {
	data, err := u.fetch(ctx, u.URL, 1<<20)
	if err != nil {
		return "ARFABIT could not check for a new version.", err
	}
	release, err := Verify(data, u.Key)
	if err != nil {
		return "ARFABIT could not check for a new version.", err
	}

	u.mu.Lock()
	u.status.CheckedAt, u.status.Latest = time.Now(), release.Version
	checked, _ := json.Marshal(map[string]any{"at": u.status.CheckedAt, "latest": release.Version})
	ready := u.status.Ready
	u.mu.Unlock()
	_ = store.WriteAtomic(filepath.Join(u.Dir, checkedFile), checked)

	if !Newer(release.Version, u.Current) {
		return "", nil
	}
	if ready != nil && ready.Version == release.Version {
		return "", nil
	}

	download, ok := release.Downloads[u.Platform]
	if !ok {
		return fmt.Sprintf("Version %s has no download for this computer.", release.Version),
			fmt.Errorf("latest.json lists downloads for %s, not %s", platforms(release), u.Platform)
	}

	u.mu.Lock()
	u.status.Working = "Downloading version " + release.Version
	u.mu.Unlock()
	u.changed()

	staged, err := u.download(ctx, release, download)
	if err != nil {
		return fmt.Sprintf("Version %s did not download.", release.Version), err
	}

	u.mu.Lock()
	old := u.status.Ready
	u.status.Ready = &staged
	u.mu.Unlock()
	if old != nil && old.File != staged.File {
		u.discard(*old)
	}
	return "", nil
}

// download fetches a release's file into the staging folder, keeps it only if
// it matches latest.json, and records it as staged.
func (u *Updater) download(ctx context.Context, release Release, d Download) (Staged, error) {
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		return Staged{}, err
	}

	name := "ARFABIT-" + release.Version + "-" + u.Platform + path.Ext(d.URL)
	partial := filepath.Join(u.Dir, ".download")
	f, err := os.Create(partial)
	if err != nil {
		return Staged{}, err
	}
	// Whatever does not match is discarded (§6).
	defer os.Remove(partial)

	hash := sha256.New()
	n, err := u.copy(ctx, d.URL, io.MultiWriter(f, hash), d.Size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Staged{}, err
	}
	if n != d.Size {
		return Staged{}, fmt.Errorf("the download was %d bytes; latest.json says %d", n, d.Size)
	}
	if sum := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(sum, d.SHA256) {
		return Staged{}, fmt.Errorf("the download's SHA-256 is %s; latest.json says %s", sum, d.SHA256)
	}

	if err := os.Rename(partial, filepath.Join(u.Dir, name)); err != nil {
		return Staged{}, err
	}
	staged := Staged{Version: release.Version, Notes: release.Notes, File: name, SHA256: strings.ToLower(d.SHA256)}
	data, _ := json.MarshalIndent(staged, "", "  ")
	if err := store.WriteAtomic(filepath.Join(u.Dir, StagedFile), append(data, '\n')); err != nil {
		return Staged{}, err
	}
	return staged, nil
}

// discard puts away a staged download that is no longer wanted. These are
// ARFABIT's own files, never the person's.
func (u *Updater) discard(s Staged) {
	if s.File != "" && filepath.Base(s.File) == s.File {
		_ = os.Remove(filepath.Join(u.Dir, s.File))
	}
	var current Staged
	if readJSON(filepath.Join(u.Dir, StagedFile), &current) == nil && current.File == s.File {
		_ = os.Remove(filepath.Join(u.Dir, StagedFile))
	}
}

// Run checks once a day while the settings allow, and calls restart once
// an update is staged, the settings allow installing it, and idle says
// nothing is running. It returns when ctx ends.
func (u *Updater) Run(ctx context.Context, settings func() store.UpdateSettings, idle func() bool, restart func()) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		s := settings()
		st := u.Status()

		u.mu.Lock()
		due := s.CheckDaily && time.Since(st.CheckedAt) > 24*time.Hour && time.Since(u.tried) > time.Hour
		u.mu.Unlock()
		if due {
			go u.Check(ctx)
		}

		if installNow(st, s, idle) {
			u.mu.Lock()
			first := !u.restarted
			u.restarted = true
			u.mu.Unlock()
			if first {
				restart()
			}
		}
	}
}

// installNow says whether to restart into the staged version: one is staged,
// it is not one that already did not start, the settings allow it, and
// nothing is running.
func installNow(st Status, s store.UpdateSettings, idle func() bool) bool {
	if st.Ready == nil || !s.InstallByThemselves {
		return false
	}
	if st.Failed != nil && st.Failed.Version == st.Ready.Version {
		return false
	}
	return idle()
}

func (u *Updater) changed() {
	if u.OnChange != nil {
		u.OnChange(u.Status())
	}
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

// fetch reads a small file, no more than limit bytes.
func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf strings.Builder
	if _, err := u.copy(ctx, url, &buf, limit); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// copy fetches url into w, refusing more than limit bytes. The request
// carries nothing but ARFABIT's version, so a server can tell it apart.
func (u *Updater) copy(ctx context.Context, url string, w io.Writer, limit int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "ARFABIT/"+u.Current)

	resp, err := u.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("%s answered %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("%s sent more than the %d bytes expected", url, limit)
	}
	return n, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	return nil
}

func platforms(r Release) string {
	var names []string
	for name := range r.Downloads {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "no platform"
	}
	return strings.Join(names, ", ")
}

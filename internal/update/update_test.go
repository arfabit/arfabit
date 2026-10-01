package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arfabit/arfabit/internal/store"
)

func TestBuiltInKeyIsAKey(t *testing.T) {
	if len(PublicKey()) != ed25519.PublicKeySize {
		t.Fatal("the built-in key is not an Ed25519 public key")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "dev", false},
		{"dev", "v0.1.0", false},
		{"v0.2", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestSignedLatestVerifies(t *testing.T) {
	pub, seed := key(t)
	data, err := Sign(Release{Version: "v0.2.0", Notes: "Faster."}, seed)
	if err != nil {
		t.Fatal(err)
	}

	r, err := Verify(data, pub)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "v0.2.0" || r.Notes != "Faster." {
		t.Errorf("release = %+v", r)
	}
}

func TestChangedLatestIsRefused(t *testing.T) {
	pub, seed := key(t)
	data, _ := Sign(Release{Version: "v0.2.0"}, seed)

	// Change one byte of what was signed.
	var env envelope
	_ = json.Unmarshal(data, &env)
	body, _ := base64.StdEncoding.DecodeString(env.Signed)
	body[len(body)/2] ^= 1
	env.Signed = base64.StdEncoding.EncodeToString(body)
	changed, _ := json.Marshal(env)

	if _, err := Verify(changed, pub); err == nil {
		t.Error("a changed latest.json was accepted")
	}

	// Signed by some other key.
	other, _ := key(t)
	if _, err := Verify(data, other); err == nil {
		t.Error("latest.json was accepted under another key")
	}
}

func TestCheckStagesANewerVersion(t *testing.T) {
	file := []byte("the new app")
	u, _ := updater(t, "v0.1.0", file, sum(file), int64(len(file)))

	u.Check(context.Background())
	st := u.Status()

	if st.Problem != "" {
		t.Fatalf("problem: %s: %s", st.Problem, st.Detail)
	}
	if st.Ready == nil || st.Ready.Version != "v0.2.0" || st.Latest != "v0.2.0" {
		t.Fatalf("status = %+v", st)
	}
	got, err := os.ReadFile(filepath.Join(u.Dir, st.Ready.File))
	if err != nil || string(got) != string(file) {
		t.Errorf("staged file = %q, %v", got, err)
	}

	// The app finds it by the staged file.
	var staged Staged
	if err := readJSON(filepath.Join(u.Dir, StagedFile), &staged); err != nil || staged.File != st.Ready.File {
		t.Errorf("staged.json = %+v, %v", staged, err)
	}
}

func TestCheckDiscardsADownloadThatDoesNotMatch(t *testing.T) {
	file := []byte("the new app")
	u, _ := updater(t, "v0.1.0", file, sum([]byte("something else")), int64(len(file)))

	u.Check(context.Background())
	st := u.Status()

	if st.Ready != nil {
		t.Fatalf("a download that did not match was staged: %+v", st.Ready)
	}
	if st.Problem != "Version v0.2.0 did not download." || !strings.Contains(st.Detail, "SHA-256") {
		t.Errorf("problem = %q / %q", st.Problem, st.Detail)
	}
	entries, _ := os.ReadDir(u.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ARFABIT-") || e.Name() == ".download" {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

func TestCheckLeavesTheSameVersionAlone(t *testing.T) {
	file := []byte("the new app")
	u, _ := updater(t, "v0.2.0", file, sum(file), int64(len(file)))

	u.Check(context.Background())
	if st := u.Status(); st.Ready != nil || st.Problem != "" || st.Latest != "v0.2.0" {
		t.Errorf("status = %+v", st)
	}
}

func TestLoadPutsAwayWhatIsInstalled(t *testing.T) {
	file := []byte("the new app")
	u, _ := updater(t, "v0.1.0", file, sum(file), int64(len(file)))
	u.Check(context.Background())
	staged := u.Status().Ready

	// The same folder, read by the version it installed.
	after := &Updater{Dir: u.Dir, Current: "v0.2.0"}
	after.Load()

	if after.Status().Ready != nil {
		t.Error("an installed version is still offered")
	}
	if _, err := os.Stat(filepath.Join(u.Dir, staged.File)); !os.IsNotExist(err) {
		t.Error("the installed version's download was kept")
	}
}

func TestInstallsOnlyWithNothingRunning(t *testing.T) {
	ready := Status{Ready: &Staged{Version: "v0.2.0"}}
	on := store.DefaultUpdateSettings
	off := store.UpdateSettings{CheckDaily: true}
	busy := func() bool { return false }
	idle := func() bool { return true }

	if installNow(ready, on, busy) {
		t.Error("would install while something runs")
	}
	if !installNow(ready, on, idle) {
		t.Error("would not install with nothing running")
	}
	if installNow(ready, off, idle) {
		t.Error("would install with Install updates by themselves off")
	}
	if installNow(Status{}, on, idle) {
		t.Error("would restart with nothing staged")
	}

	failed := ready
	failed.Failed = &Failed{Version: "v0.2.0"}
	if installNow(failed, on, idle) {
		t.Error("would install again a version that did not start")
	}
}

// --- helpers -------------------------------------------------------------

func key(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv.Seed()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// updater is an Updater for a copy at current, whose latest.json offers
// v0.2.0 with file as its Mac download, described by sha and size. Requests
// are answered here: nothing listens on a port.
func updater(t *testing.T, current string, file []byte, sha string, size int64) (*Updater, ed25519.PublicKey) {
	t.Helper()
	pub, seed := key(t)

	latest, err := Sign(Release{
		Version: "v0.2.0",
		Notes:   "Faster.",
		Downloads: map[string]Download{
			"darwin": {URL: "https://example.invalid/ARFABIT-macOS.zip", SHA256: sha, Size: size},
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"https://example.invalid/latest.json":       latest,
		"https://example.invalid/ARFABIT-macOS.zip": file,
	}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body, ok := files[r.URL.String()]
		if !ok {
			return &http.Response{StatusCode: 404, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})}

	u := &Updater{
		Dir:      t.TempDir(),
		Current:  current,
		Platform: "darwin",
		URL:      "https://example.invalid/latest.json",
		Key:      pub,
		Client:   client,
	}
	u.Load()
	return u, pub
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

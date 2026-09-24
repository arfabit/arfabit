package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The page's script must run to completion against the page it ships with.
//
// It died at load three times — a handler wired to a removed button, a
// variable used before it was declared, and two functions deleted while
// something still called them. Each time the only sign was a banner after the
// fact, and each time the rest of the page silently did nothing.
//
// This runs the real script against a stubbed browser and the real template.
func TestPageScriptRunsWithoutThrowing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed, so the page script cannot be exercised here")
	}

	dir := t.TempDir()

	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := templates.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(dir, "app.js")
	pagePath := filepath.Join(dir, "index.html")
	for path, content := range map[string][]byte{scriptPath: script, pagePath: page} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := exec.Command(node, "testdata/smoke.js", scriptPath, pagePath).CombinedOutput()
	if err != nil {
		t.Fatalf("the page script did not run cleanly:\n%s", out)
	}
}

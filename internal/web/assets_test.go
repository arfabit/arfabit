package web

import (
	"regexp"
	"strings"
	"testing"
)

// Every element the script wires a handler to must exist in the page.
//
// A missing one used to throw at load and stop every later line from running,
// so a button removed from the template took the rest of the page down with
// it — twice. The wiring is defensive now, but a gap is still a bug and this
// is where it gets caught.
func TestEveryWiredElementExists(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := templates.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}

	wiring := regexp.MustCompile(`on\("([a-z0-9-]+)",`)
	html := string(page)

	seen := map[string]bool{}
	for _, match := range wiring.FindAllStringSubmatch(string(script), -1) {
		id := match[1]
		if seen[id] {
			continue
		}
		seen[id] = true

		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("the script wires %q, which is not in the page", id)
		}
	}

	if len(seen) == 0 {
		t.Fatal("no wiring was found; the test is not checking anything")
	}
}

// The script must not reach for an element by a name the page does not use.
func TestEveryLookedUpElementExists(t *testing.T) {
	script, err := assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	page, err := templates.ReadFile("templates/index.html")
	if err != nil {
		t.Fatal(err)
	}

	lookup := regexp.MustCompile(`\$\("([a-z0-9-]+)"\)`)
	html := string(page)

	// Created by the script rather than written in the page.
	made := map[string]bool{"page-problem": true}

	for _, match := range lookup.FindAllStringSubmatch(string(script), -1) {
		id := match[1]
		if made[id] || strings.Contains(html, `id="`+id+`"`) {
			continue
		}
		t.Errorf("the script looks for %q, which is not in the page", id)
	}
}

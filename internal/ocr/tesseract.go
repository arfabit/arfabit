package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// tesseract reads with Tesseract, where somebody has installed it. ARFABIT
// does not install it; it uses it when it is there.
//
// One run reads a whole chunk: given a file listing pictures, Tesseract
// reads each as a page, and its TSV output numbers the pages from 1.
type tesseract struct{ bin string }

func (t tesseract) Read(ctx context.Context, pictures []string, lang string) ([]Line, error) {
	if len(pictures) == 0 {
		return nil, nil
	}

	// --psm 6 reads the picture as one block of text, which a subtitle is.
	args := []string{"", "stdout", "--psm", "6"}
	if l, known := languageOf(lang); known {
		have, err := t.languages(ctx)
		if err != nil {
			return nil, err
		}
		var use []string
		for _, name := range strings.Split(l.tess, "+") {
			if have[name] {
				use = append(use, name)
			}
		}
		if len(use) == 0 {
			return nil, &LanguageError{Lang: lang, Message: "Tesseract on this computer has no " + l.name +
				" language data, so ARFABIT cannot read " + l.name + " subtitles into text. They can be copied as they are."}
		}
		args = append(args, "-l", strings.Join(use, "+"))
	}
	args = append(args, "tsv")

	list := filepath.Join(filepath.Dir(pictures[0]), "pictures.txt")
	if err := os.WriteFile(list, []byte(strings.Join(pictures, "\n")+"\n"), 0o644); err != nil {
		return nil, err
	}
	args[0] = list

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, t.bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v\n%s", err, stderr.String())
	}
	lines, err := parseTSV(stdout.String(), len(pictures))
	if err != nil {
		return nil, fmt.Errorf("%v\n%s", err, stderr.String())
	}
	return lines, nil
}

// languages lists the language data Tesseract has here.
func (t tesseract) languages(ctx context.Context) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, t.bin, "--list-langs").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%v\n%s", err, out)
	}
	have := map[string]bool{}
	for i, line := range strings.Split(string(out), "\n") {
		// The first line says where the data is kept.
		if i > 0 && strings.TrimSpace(line) != "" {
			have[strings.TrimSpace(line)] = true
		}
	}
	return have, nil
}

// parseTSV turns Tesseract's TSV into a Line per page. Words (level 5) are
// joined by spaces within a line, and lines by newlines.
func parseTSV(tsv string, pages int) ([]Line, error) {
	type page struct {
		lines []string
		last  string
	}
	found := make([]page, pages)

	for _, row := range strings.Split(tsv, "\n") {
		f := strings.Split(strings.TrimRight(row, "\r"), "\t")
		if len(f) < 12 || f[0] != "5" {
			continue
		}
		text := strings.TrimSpace(f[11])
		if text == "" {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 || n > pages {
			return nil, fmt.Errorf("Tesseract answered for page %q of %d", f[1], pages)
		}
		p := &found[n-1]

		key := f[2] + "." + f[3] + "." + f[4] // block, paragraph, line
		if key != p.last || len(p.lines) == 0 {
			p.lines = append(p.lines, text)
			p.last = key
		} else {
			p.lines[len(p.lines)-1] += " " + text
		}
	}

	lines := make([]Line, pages)
	for i, p := range found {
		lines[i] = Line{Text: strings.Join(p.lines, "\n")}
	}
	return lines, nil
}

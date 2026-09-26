package ocr

import (
	_ "embed"
	"os/exec"
)

//go:embed vision.js
var visionScript []byte

// NotAvailable is what the page says where there is no reader.
const NotAvailable = "Subtitles cannot be read into text on this computer, so they can only be copied as they are."

// System is the reader this computer has: Vision, through osascript, which
// every Mac has.
func System() Reader {
	bin, err := exec.LookPath("osascript")
	if err != nil {
		return nil
	}
	return script{
		name: "vision.js",
		body: visionScript,
		command: func(script, list, out, tag string) (string, []string) {
			return bin, []string{"-l", "JavaScript", script, list, out, tag}
		},
		missing: func(name string) string {
			return "This Mac cannot read " + name + " text, so " + name + " subtitles can only be copied as they are."
		},
	}
}

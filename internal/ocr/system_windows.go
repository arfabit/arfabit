package ocr

import (
	_ "embed"
	"os/exec"
)

//go:embed windows.ps1
var windowsScript []byte

// NotAvailable is what the page says where there is no reader.
const NotAvailable = "Subtitles cannot be read into text on this computer, so they can only be copied as they are."

// System is the reader this computer has: Windows.Media.Ocr, through Windows
// PowerShell, which every Windows 10 and 11 computer has.
func System() Reader {
	bin, err := exec.LookPath("powershell.exe")
	if err != nil {
		return nil
	}
	return script{
		name: "windows.ps1",
		body: windowsScript,
		command: func(script, list, out, tag string) (string, []string) {
			return bin, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-File", script, "-List", list, "-Out", out, "-Lang", tag}
		},
		missing: func(name string) string {
			if name == "" {
				return "No language pack for Windows that reads text is installed on this computer, so ARFABIT cannot read these subtitles into text. They can be copied as they are."
			}
			return "The " + name + " language pack for Windows is not installed on this computer, so ARFABIT cannot read " +
				name + " subtitles into text. They can be copied as they are."
		},
	}
}

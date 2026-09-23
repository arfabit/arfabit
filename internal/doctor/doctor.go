// Package doctor checks that everything ARFABIT needs is present.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/ffmpeg"
)

// Status is how a check turned out.
type Status string

const (
	// StatusOK means nothing needs doing.
	StatusOK Status = "ok"

	// StatusMissing means something needs installing, and ARFABIT knows how.
	StatusMissing Status = "missing"

	// StatusWarn means it works but something is worth knowing.
	StatusWarn Status = "warn"
)

// Check is one thing ARFABIT looked at.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`

	// Message is plain language, addressed to the person reading it.
	Message string `json:"message"`

	// Fix is what to do, written so it can be followed without knowing
	// anything about any of this.
	Fix string `json:"fix,omitempty"`

	// Command is a fix ARFABIT could run itself, when that is safe.
	Command string `json:"command,omitempty"`

	// Detail is the raw finding, shown only on request.
	Detail string `json:"detail,omitempty"`
}

// Report is every check, in the order they matter.
type Report struct {
	Checks []Check `json:"checks"`
}

// Ready reports whether a disc could be ripped right now.
func (r Report) Ready() bool {
	for _, c := range r.Checks {
		if c.Status == StatusMissing {
			return false
		}
	}
	return true
}

// Run performs every check.
func Run(_ context.Context, cfg config.Config) Report {
	var r Report

	// Folders first, and deliberately.
	//
	// Creating them is what makes macOS ask permission for the Downloads
	// folder, and that question should arrive while ARFABIT is plainly
	// starting up rather than minutes later, next to whatever the person
	// happened to click. Everything after it is quick by comparison, except
	// the drive, which is last because spinning up a disc takes seconds.
	r.Checks = append(r.Checks, checkFolders(cfg))
	r.Checks = append(r.Checks, checkMakeMKV())
	r.Checks = append(r.Checks, checkTool("FFmpeg", ffmpeg.Locate, installFFmpeg()))
	r.Checks = append(r.Checks, checkTool("FFprobe", ffmpeg.LocateProbe, installFFmpeg()))
	r.Checks = append(r.Checks, checkLicense())

	// The drive is deliberately not checked here.
	//
	// ARFABIT watches it continuously and says what it holds on the main
	// card, which is both live and more useful. Checking it again would
	// report the same thing twice, once of them out of date, and asking
	// makemkvcon was also the slowest thing Doctor did.
	return r
}

// checkMakeMKV looks for makemkvcon.
//
// PATH alone is not enough: the MakeMKV installer does not add it on macOS or
// Windows, so looking only there tells most people it is missing when it is
// not.
func checkMakeMKV() Check {
	path, err := makemkv.Locate()
	if err != nil {
		return Check{
			Name:    "MakeMKV",
			Status:  StatusMissing,
			Message: "MakeMKV reads the disc. It is not installed yet.",
			Fix:     installMakeMKVText(),
			Command: installMakeMKVCommand(),
		}
	}
	return Check{
		Name:    "MakeMKV",
		Status:  StatusOK,
		Message: "MakeMKV is ready.",
		Detail:  path,
	}
}

func checkTool(name string, locate func() (string, error), fix Check) Check {
	path, err := locate()
	if err != nil {
		fix.Name = name
		fix.Status = StatusMissing
		fix.Message = fmt.Sprintf("%s makes the movie file. It is not installed yet.", name)
		return fix
	}
	return Check{Name: name, Status: StatusOK, Message: name + " is ready.", Detail: path}
}

func installFFmpeg() Check {
	switch runtime.GOOS {
	case "darwin":
		return Check{Fix: "Install it with Homebrew.", Command: "brew install ffmpeg"}
	case "windows":
		return Check{Fix: "Install it with winget.", Command: "winget install ffmpeg"}
	default:
		return Check{Fix: "Install it with your package manager.", Command: "sudo apt install ffmpeg"}
	}
}

func installMakeMKVText() string {
	switch runtime.GOOS {
	case "darwin":
		return "Install MakeMKV with Homebrew, then open it once so it can set itself up."
	case "windows":
		return "Download MakeMKV from makemkv.com and install it, then open it once."
	default:
		return "Install MakeMKV for your system, then open it once so it can set itself up."
	}
}

func installMakeMKVCommand() string {
	if runtime.GOOS == "darwin" {
		return "brew install --cask makemkv"
	}
	return ""
}

// licenseWarningWindow is how long before expiry the key is worth mentioning.
const licenseWarningWindow = 7 * 24 * time.Hour

// checkLicense looks at MakeMKV's settings for a key.
//
// Beta keys expire about every two months, and a key that quietly ran out is
// the failure people waste the most time on.
func checkLicense() Check {
	path, err := settingsPath()
	if err != nil {
		return Check{Name: "MakeMKV key", Status: StatusWarn, Message: "ARFABIT could not find MakeMKV's settings."}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Check{
			Name:    "MakeMKV key",
			Status:  StatusWarn,
			Message: "MakeMKV has not been opened yet. Open it once so it can set itself up.",
			Detail:  path,
		}
	}

	if !strings.Contains(string(data), "app_Key") {
		return Check{
			Name:    "MakeMKV key",
			Status:  StatusWarn,
			Message: "MakeMKV does not have a key yet. The free beta key works for Blu-ray discs.",
			Fix:     "Get the current beta key from the MakeMKV forum, or buy one.",
			Detail:  path,
		}
	}

	return Check{Name: "MakeMKV key", Status: StatusOK, Message: "MakeMKV has a key.", Detail: path}
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "MakeMKV", "settings.conf"), nil
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "MakeMKV", "settings.conf"), nil
	default:
		return filepath.Join(home, ".MakeMKV", "settings.conf"), nil
	}
}

// checkFolders makes sure ARFABIT can write where it intends to.
func checkFolders(cfg config.Config) Check {
	for _, dir := range []string{cfg.Paths.Masters, cfg.Paths.Library, cfg.Paths.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Check{
				Name:    "Folders",
				Status:  StatusMissing,
				Message: fmt.Sprintf("ARFABIT cannot use the folder %s.", dir),
				Fix:     "Choose a different folder in Settings, or check the folder's permissions.",
				Detail:  err.Error(),
			}
		}
	}
	return Check{
		Name:    "Folders",
		Status:  StatusOK,
		Message: fmt.Sprintf("Your movies will go in %s.", cfg.Paths.Library),
	}
}

//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// label identifies the entry to launchd.
const label = "com.arfabit.arfabit"

// A LaunchAgent, not a LaunchDaemon. ARFABIT needs the user's session: the
// optical drive and the user's own folders are not reachable from a system
// daemon.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-no-open</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "arfabit.log")
}

func enable(exe string) (Status, error) {
	path := plistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}

	content := fmt.Sprintf(plistTemplate, label, exe, logPath(), logPath())
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Status{}, err
	}

	// bootstrap is the current way in; load is kept as a fallback for older
	// systems. Neither failing is fatal, because the entry is written and will
	// take effect at the next login regardless.
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("launchctl", "bootstrap", uid, path).Run(); err != nil {
		_ = exec.Command("launchctl", "load", "-w", path).Run()
	}

	return current(), nil
}

func disable() (Status, error) {
	path := plistPath()

	uid := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("launchctl", "bootout", uid+"/"+label).Run(); err != nil {
		_ = exec.Command("launchctl", "unload", "-w", path).Run()
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return Status{}, err
	}
	return current(), nil
}

func current() Status {
	path := plistPath()
	_, err := os.Stat(path)
	return Status{Enabled: err == nil, Path: path, Mechanism: "launchd user agent"}
}

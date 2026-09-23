//go:build linux

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const unitName = "arfabit.service"

// A user unit rather than a system one, for the same reason as on macOS: the
// drive and the user's folders belong to the session. Lingering is what keeps
// it running after logout.
const unitTemplate = `[Unit]
Description=ARFABIT

[Service]
ExecStart=%s -no-open
Restart=on-failure

[Install]
WantedBy=default.target
`

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", unitName)
}

func enable(exe string) (Status, error) {
	path := unitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(unitTemplate, exe)), 0o644); err != nil {
		return Status{}, err
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	_ = exec.Command("systemctl", "--user", "enable", "--now", unitName).Run()

	// Without lingering, the service stops when the user logs out.
	if user := os.Getenv("USER"); user != "" {
		_ = exec.Command("loginctl", "enable-linger", user).Run()
	}

	return current(), nil
}

func disable() (Status, error) {
	_ = exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()

	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return Status{}, err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	return current(), nil
}

func current() Status {
	path := unitPath()
	_, err := os.Stat(path)
	return Status{Enabled: err == nil, Path: path, Mechanism: "systemd user service"}
}

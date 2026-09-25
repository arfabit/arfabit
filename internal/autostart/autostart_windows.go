//go:build windows

package autostart

import (
	"fmt"
	"os"
	"os/exec"
)

const taskName = "ARFABIT"

// A Task Scheduler entry rather than a Run-key value: it survives better, and is
// visible somewhere a person can find it. It does not restart ARFABIT when it
// exits, so that Stop stays stopped.
func enable(exe string) (Status, error) {
	cmd := exec.Command("schtasks", "/Create", "/F",
		"/TN", taskName,
		"/TR", fmt.Sprintf(`"%s" -no-open`, exe),
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Status{}, fmt.Errorf("%s", string(out))
	}
	return current(), nil
}

func disable() (Status, error) {
	cmd := exec.Command("schtasks", "/Delete", "/F", "/TN", taskName)
	if out, err := cmd.CombinedOutput(); err != nil && !os.IsNotExist(err) {
		return Status{}, fmt.Errorf("%s", string(out))
	}
	return current(), nil
}

func current() Status {
	err := exec.Command("schtasks", "/Query", "/TN", taskName).Run()
	return Status{Enabled: err == nil, Path: taskName, Mechanism: "Task Scheduler at logon"}
}

// startupTemplate exposes the entry for testing. Windows builds its command
// line directly rather than from a template.
func startupTemplate() string { return "" }

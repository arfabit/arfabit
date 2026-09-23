//go:build windows

package restart

import (
	"os"
	osexec "os/exec"
)

// exec starts a fresh copy and stands down.
//
// Windows has no way to replace a running program in place, so the new copy is
// started as its own process and this one exits. The port is freed as this
// process ends, which is why the new copy retries briefly on startup.
func exec(path string, args, env []string) error {
	cmd := osexec.Command(path, args[1:]...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	os.Exit(0)
	return nil
}

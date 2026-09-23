//go:build !windows

package restart

import "syscall"

// exec replaces the running program with a new copy of itself.
//
// The process keeps its identity, so anything watching it — launchd, systemd,
// a terminal — sees a program that never stopped. Open sockets are closed by
// the exec itself, which is what frees the port for the new copy.
func exec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}

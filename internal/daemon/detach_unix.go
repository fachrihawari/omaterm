//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon in a session of its own, so closing the app or
// its terminal does not end it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

//go:build !windows

package loginenv

import (
	"os/exec"
	"syscall"
)

// inGroup runs the shell in a process group of its own, so that what its
// profile starts ends with it.
func inGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup ends the shell and everything it started.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// inGroup runs a command in a process group of its own, so it and the
// helpers it spawns end together.
func inGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup ends a command and everything it spawned.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// A negative pid is the group; SIGKILL, as nothing it cleans up
	// matters and it may be wedged.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

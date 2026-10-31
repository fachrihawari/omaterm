//go:build windows

package tunnel

import (
	"os/exec"
	"strconv"
)

func inGroup(*exec.Cmd) {}

// killGroup ends a command and everything it spawned: taskkill takes
// the process tree.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

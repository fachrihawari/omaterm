package loginenv

import "os/exec"

// Windows has no login shell to read: Import never runs one there.
func inGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

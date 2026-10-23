package daemon

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon without a console of its own.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | 0x00000008}
}

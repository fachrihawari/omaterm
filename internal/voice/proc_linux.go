package voice

import (
	"os/exec"
	"syscall"
)

// setProcAttr has whisper-server killed with the daemon that started it,
// should the daemon die without stopping it.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

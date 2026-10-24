// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/git/hide_windows.go.

package gitsnap

import (
	"os/exec"
	"syscall"
)

// createNoWindow is Windows' CREATE_NO_WINDOW process creation flag.
const createNoWindow = 0x08000000

// hide starts git without a console: the app has none, and each run would
// open one.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

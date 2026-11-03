//go:build !linux

package voice

import "os/exec"

// setProcAttr does nothing here: no system call ties a child's life to its
// parent's, and the daemon stops whisper-server as it stops.
func setProcAttr(*exec.Cmd) {}

//go:build !windows

package gitsnap

import "os/exec"

func hide(*exec.Cmd) {}

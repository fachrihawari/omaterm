//go:build !windows

package tunnel

import "os/exec"

func hide(*exec.Cmd) {}

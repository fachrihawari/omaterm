//go:build !windows

package web

import "os/exec"

func hide(*exec.Cmd) {}

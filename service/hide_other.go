//go:build !windows

package service

import "os/exec"

func hide(*exec.Cmd) {}

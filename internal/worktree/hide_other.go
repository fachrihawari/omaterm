//go:build !windows

package worktree

import "os/exec"

func hide(*exec.Cmd) {}

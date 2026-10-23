//go:build !windows

package ctl

import "syscall"

func umask(m int) int { return syscall.Umask(m) }

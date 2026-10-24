//go:build !windows

// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/git/hide_other.go.

package gitsnap

import "os/exec"

func hide(*exec.Cmd) {}

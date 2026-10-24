// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/proc_linux.go.

package pty

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// ProcName returns a process's name, as ps shows it.
func ProcName(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ProcArgs returns the arguments a process was started with.
func ProcArgs(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}

// ProcCwd returns a process's working directory.
func ProcCwd(pid int) string {
	dir, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	return dir
}

// ProcParent returns a process's parent, 0 when it is gone: the field of
// /proc/pid/stat after its state, past the name in parentheses.
func ProcParent(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}

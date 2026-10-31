//go:build linux || darwin

package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestProcLookups reads a process the test starts, as a pane reads its
// foreground program: on Linux from /proc, on macOS through libSystem.
func TestProcLookups(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OMATERM_PROC_TEST=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	pid := cmd.Process.Pid
	// Linux renames a process during exec a moment before its new
	// arguments and environment are readable: wait for both.
	deadline := time.Now().Add(2 * time.Second)
	ready := func() bool {
		return ProcName(pid) == "sleep" && len(ProcArgs(pid)) == 2 && slices.Contains(ProcEnv(pid), "OMATERM_PROC_TEST=1")
	}
	for !ready() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if n := ProcName(pid); n != "sleep" {
		t.Errorf("name %q", n)
	}
	if a := ProcArgs(pid); len(a) != 2 || a[1] != "30" {
		t.Errorf("args %q", a)
	}
	if !slices.Contains(ProcEnv(pid), "OMATERM_PROC_TEST=1") {
		t.Error("the environment misses OMATERM_PROC_TEST")
	}
	want, _ := filepath.EvalSymlinks(dir) // macOS's /var is /private/var
	if got, _ := filepath.EvalSymlinks(ProcCwd(pid)); got != want {
		t.Errorf("cwd %q, want %q", got, want)
	}
	if p := ProcParent(pid); p != os.Getpid() {
		t.Errorf("parent %d, want %d", p, os.Getpid())
	}
	if !slices.Contains(Procs(), pid) {
		t.Error("Procs misses the process")
	}
	if ProcName(1<<30) != "" || ProcParent(1<<30) != 0 || ProcCwd(1<<30) != "" || ProcArgs(1<<30) != nil {
		t.Error("a process that isn't there reads as something")
	}
}

//go:build linux || darwin

package daemon

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

// ended tells whether a process has ended, within a moment: a zombie not yet
// reaped has ended too.
func ended(pid int, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err != nil || strings.Contains(string(b), ") Z ") {
			return true
		}
		if runtime.GOOS != "linux" {
			if syscall.Kill(pid, 0) != nil {
				return true
			}
		}
	}
	return false
}

// leakFixture is a daemon with a process whose program starts a child that
// ignores the hangup, as a dev server wrapped by a package manager can, and
// notes the child's pid in a file.
func leakFixture(t *testing.T) (*Daemon, *client.Client, string) {
	t.Helper()
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := "trap '' HUP\nsleep 300 &\necho $! > " + pidFile + ".tmp && mv " + pidFile + ".tmp " + pidFile + "\nwait\n"
	if err := os.WriteFile(filepath.Join(projectDir, "leak.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	writeProcesses(t, projectDir, "processes:\n  web:\n    command: sh leak.sh\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: "web"})
	return d, c, pidFile
}

// childOf waits for the child the process started, and returns its pid.
func childOf(t *testing.T, pidFile string) int {
	t.Helper()
	var pid int
	waitFor(t, "the process's child", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return pid > 0
	})
	return pid
}

// A program a project process started doesn't outlive the daemon: its
// whole process group ends when the daemon stops, even a child that ignores
// the hangup.
func TestDaemonStopEndsAProcessesChildren(t *testing.T) {
	d, c, pidFile := leakFixture(t)
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	child := childOf(t, pidFile)
	if syscall.Kill(child, 0) != nil {
		t.Fatal("the child isn't running before the stop")
	}
	start := time.Now()
	d.Stop()
	// At once: the stop waited for it, as the daemon's process leaves with it.
	if !ended(child, 300*time.Millisecond) {
		t.Fatalf("child %d outlived the daemon's stop", child)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Fatalf("the stop took %v", took)
	}
}

// A process's own stop and restart end its program's group too.
func TestProcessStopAndRestartEndTheirChildren(t *testing.T) {
	_, c, pidFile := leakFixture(t)
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	first := childOf(t, pidFile)
	call[proto.Process](t, c, "process.stop", proto.ProcessParams{Project: "p1", Name: "web"})
	if !ended(first, time.Second) {
		t.Fatalf("child %d outlived the process's stop", first)
	}

	os.Remove(pidFile)
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	second := childOf(t, pidFile)
	if second == first {
		t.Fatal("the same child twice")
	}
	os.Remove(pidFile)
	call[proto.Process](t, c, "process.restart", proto.ProcessParams{Project: "p1", Name: "web"})
	if !ended(second, time.Second) {
		t.Fatalf("child %d outlived the process's restart", second)
	}
	third := childOf(t, pidFile)
	if third == second {
		t.Fatal("the restart kept the old child")
	}
	call[proto.Process](t, c, "process.stop", proto.ProcessParams{Project: "p1", Name: "web"})
	if !ended(third, time.Second) {
		t.Fatalf("child %d outlived the last stop", third)
	}
}

// The daemon catches SIGHUP and SIGPIPE rather than ignoring them: an
// ignored signal stays ignored in every program it starts, which is why a
// process's hangup never reached it.
func TestDaemonSignalsAreCaughtNotIgnored(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	// As a daemon started by something that ignored them.
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	notifySignals()
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP, syscall.SIGPIPE, syscall.SIGTERM, syscall.SIGINT) })
	out, err := exec.Command("sh", "-c", "grep SigIgn /proc/self/status").Output()
	if err != nil {
		t.Fatal(err)
	}
	mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "SigIgn:")), 16, 64)
	if err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGPIPE} {
		if mask&(1<<(uint(sig)-1)) != 0 {
			t.Errorf("%v is ignored in a program the daemon starts (mask %x)", sig, mask)
		}
	}
}

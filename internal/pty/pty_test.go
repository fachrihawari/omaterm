//go:build linux

package pty

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, p *Proc) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		sb.Write(buf[:n])
		if err == io.EOF {
			return sb.String()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func start(t *testing.T, script string) *Proc {
	t.Helper()
	p, err := Start(Options{Path: "/bin/sh", Args: []string{"sh", "-c", script}, Env: os.Environ(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOutputAndExitCode(t *testing.T) {
	p := start(t, "printf hi; exit 3")
	if out := readAll(t, p); !strings.Contains(out, "hi") {
		t.Errorf("output %q", out)
	}
	if c := p.Code(); c != 3 {
		t.Errorf("code %d", c)
	}
}

func TestResize(t *testing.T) {
	p := start(t, "sleep 0.2; stty size")
	if err := p.Resize(100, 40, 0, 0); err != nil {
		t.Fatal(err)
	}
	if out := readAll(t, p); !strings.Contains(out, "40 100") {
		t.Errorf("stty size: %q", out)
	}
}

func TestForegroundAndProc(t *testing.T) {
	p := start(t, "exec sleep 5")
	defer p.Kill()
	deadline := time.Now().Add(2 * time.Second)
	for ProcName(p.PID()) != "sleep" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fg := p.Foreground(); fg != p.PID() {
		t.Errorf("foreground %d, pid %d", fg, p.PID())
	}
	if n := ProcName(p.PID()); n != "sleep" {
		t.Errorf("name %q", n)
	}
	if a := ProcArgs(p.PID()); len(a) != 2 || a[1] != "5" {
		t.Errorf("args %q", a)
	}
	if ProcParent(p.PID()) != os.Getpid() {
		t.Error("parent is not the test")
	}
	if ProcCwd(p.PID()) == "" {
		t.Error("no cwd")
	}
}

func TestKill(t *testing.T) {
	p := start(t, "exec sleep 30")
	p.Kill()
	if c := p.Code(); c != 137 {
		t.Errorf("code %d, want 137", c)
	}
}

func TestNoFDLeak(t *testing.T) {
	count := func() int { e, _ := os.ReadDir("/proc/self/fd"); return len(e) }
	before := count()
	for i := 0; i < 5; i++ {
		p := start(t, "true")
		readAll(t, p)
		p.Code()
		p.Close()
	}
	if after := count(); after > before+1 {
		t.Errorf("fds %d → %d", before, after)
	}
}

// gone tells whether a process has ended, within a moment: one that is a
// zombie, not yet reaped, has ended too.
func gone(pid int) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil || strings.Contains(string(b), ") Z ") {
			return true
		}
	}
	return false
}

// childPID reads the pid a script noted.
func childPID(t *testing.T, file string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(file); err == nil && strings.HasSuffix(string(b), "\n") {
			n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return n
		}
	}
	t.Fatal("the script never noted its child")
	return 0
}

// Stop ends the program and what it left in its group: a child that
// ignores the hangup, one that ignores the terminate too (killed after the
// grace), and one left behind after the program exited.
func TestStopEndsTheWholeGroup(t *testing.T) {
	for _, tt := range []struct {
		name, script string
		took         time.Duration // at least
	}{
		{"a child ignoring the hangup", `trap "" HUP; sleep 300 & echo $! > FILE; wait`, 0},
		{"a program and child ignoring terminate", `trap "" HUP TERM; sleep 300 & echo $! > FILE; wait`, 300 * time.Millisecond},
		{"a child left after the program exited", `(trap "" HUP TERM; exec sleep 300) & echo $! > FILE`, 300 * time.Millisecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "pid")
			p := start(t, strings.ReplaceAll(tt.script, "FILE", file))
			pid := childPID(t, file)
			p.Close() // the hangup: ignored by all of them
			start := time.Now()
			p.Stop(400 * time.Millisecond)
			if took := time.Since(start); took < tt.took {
				t.Errorf("Stop returned after %v, before the grace for what ignores terminate", took)
			}
			if !gone(pid) {
				t.Fatalf("child %d is still running", pid)
			}
			select {
			case <-p.Exited():
			case <-time.After(2 * time.Second):
				t.Fatal("the program is still running")
			}
			// Nothing left to signal: a second Stop does nothing, quickly.
			start = time.Now()
			p.Stop(2 * time.Second)
			if time.Since(start) > 500*time.Millisecond {
				t.Error("Stop waited for a group that was gone")
			}
		})
	}
}

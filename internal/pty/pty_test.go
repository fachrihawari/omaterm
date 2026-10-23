//go:build linux

package pty

import (
	"io"
	"os"
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

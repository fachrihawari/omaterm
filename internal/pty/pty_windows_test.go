package pty

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// started runs cmd's line in a console, its output read meanwhile: a
// console whose output nobody reads stops its programs.
func started(t *testing.T, flag, line string) (*Proc, func() string) {
	t.Helper()
	comspec := os.Getenv("ComSpec")
	p, err := Start(Options{Path: comspec, Args: []string{"cmd", "/d", flag, line}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := p.Read(b)
			mu.Lock()
			out.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return p, func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
}

func TestConsoleRunsAProgram(t *testing.T) {
	p, out := started(t, "/c", `echo hello "world" & exit 3`)
	select {
	case <-p.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("the program didn't exit")
	}
	if p.Code() != 3 {
		t.Errorf("code %d, want 3", p.Code())
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out(), `hello "world"`) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out(), `hello "world"`) {
		t.Errorf("output %q", out())
	}
	// The console closed as the program exited: reading ends.
	if _, err := p.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("Read after exit: %v, want EOF", err)
	}
}

// What the shell runs is its foreground program, and closing the pane
// ends it with the shell.
func TestConsoleForegroundAndStop(t *testing.T) {
	p, _ := started(t, "/k", "ping -n 60 127.0.0.1")
	var fg int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if fg = p.Foreground(); fg != p.PID() && ProcName(fg) == "ping" {
			break
		}
	}
	if ProcName(fg) != "ping" || ProcParent(fg) != p.PID() {
		t.Fatalf("foreground %d (%q, parent %d), want the ping cmd %d runs", fg, ProcName(fg), ProcParent(fg), p.PID())
	}
	if args := ProcArgs(fg); len(args) != 4 || args[1] != "-n" || args[3] != "127.0.0.1" {
		t.Errorf("ProcArgs = %q", args)
	}
	if !p.jobPIDs()[fg] {
		t.Errorf("ping %d isn't in the pane's job %v", fg, p.jobPIDs())
	}

	p.Close()
	p.Stop(2 * time.Second)
	select {
	case <-p.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("the shell outlived its pane")
	}
	if alive(fg) {
		t.Error("ping outlived its pane")
	}
}

// Kill ends what the program started too, even when it left the console.
func TestConsoleKillEndsTheJob(t *testing.T) {
	p, _ := started(t, "/c", "start /b ping -n 60 127.0.0.1 >nul & ping -n 60 127.0.0.1 >nul")
	var pids []int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && len(pids) < 2; time.Sleep(100 * time.Millisecond) {
		pids = pids[:0]
		for pid := range p.jobPIDs() {
			if pid != p.PID() {
				pids = append(pids, pid)
			}
		}
	}
	if len(pids) < 2 {
		t.Fatalf("job holds %v, want two pings beside cmd", p.jobPIDs())
	}
	p.Kill()
	<-p.Exited()
	time.Sleep(200 * time.Millisecond)
	for _, pid := range pids {
		if alive(pid) {
			t.Errorf("%d (%s) outlived Kill", pid, ProcName(pid))
		}
	}
}

func TestProcLookupsOfThisProcess(t *testing.T) {
	me := os.Getpid()
	if name := ProcName(me); !strings.HasSuffix(name, ".test") {
		t.Errorf("ProcName = %q", name)
	}
	if ProcParent(me) <= 0 {
		t.Errorf("ProcParent = %d", ProcParent(me))
	}
	args := ProcArgs(me)
	if len(args) != len(os.Args) || !strings.EqualFold(exeName(args[0]), exeName(os.Args[0])) {
		t.Errorf("ProcArgs = %q, want %q", args, os.Args)
	}
	found := false
	for _, pid := range Procs() {
		found = found || pid == me
	}
	if !found {
		t.Error("Procs lacks this process")
	}
}

// alive tells whether a process still runs.
func alive(pid int) bool {
	const synchronize = 0x00100000
	h, err := syscall.OpenProcess(synchronize, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	ev, _ := syscall.WaitForSingleObject(h, 0)
	return ev == syscall.WAIT_TIMEOUT
}

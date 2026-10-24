package service

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestListProjectProcesses(t *testing.T) {
	dir := t.TempDir()
	// Start a child process with CWD in dir.
	cmd := exec.Command("sleep", "60")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// Give it a moment to appear in /proc.
	time.Sleep(100 * time.Millisecond)

	procs, err := ListProjectProcesses(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range procs {
		if p.Pid == cmd.Process.Pid {
			found = true
			if p.Name != "sleep" {
				t.Errorf("name = %q, want sleep", p.Name)
			}
		}
	}
	if !found {
		t.Fatalf("process %d not found in project processes", cmd.Process.Pid)
	}

	// After killing, it should disappear (eventually).
	cmd.Process.Kill()
	cmd.Wait()
	time.Sleep(100 * time.Millisecond)
	procs2, err := ListProjectProcesses(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs2 {
		if p.Pid == cmd.Process.Pid {
			t.Fatal("killed process still listed")
		}
	}
}

func TestListProjectPorts(t *testing.T) {
	dir := t.TempDir()
	// Start a listener in the project dir.
	cmd := exec.Command("python3", "-c", "import socket,time; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(5); time.sleep(10)")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Skip("python3 not available")
	}
	defer cmd.Process.Kill()

	time.Sleep(500 * time.Millisecond)

	procs, _ := ListProjectProcesses(dir, nil)
	ports, err := ListProjectPorts(procs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) == 0 {
		t.Fatal("expected at least one listening port")
	}
	t.Logf("found %d ports", len(ports))
}

func TestKillProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	if err := KillProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
}

// A project lists its panes' shells and what they start, wherever that
// runs, and nothing from outside it.
func TestListProjectProcessesRoots(t *testing.T) {
	workspace, elsewhere := t.TempDir(), t.TempDir()
	start := func() *exec.Cmd {
		cmd := exec.Command("sleep", "30")
		cmd.Dir = elsewhere
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd
	}
	mine, other := start(), start()
	time.Sleep(100 * time.Millisecond)

	procs, err := ListProjectProcesses(workspace, []int{mine.Process.Pid})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[int]bool{}
	for _, p := range procs {
		listed[p.Pid] = true
	}
	if !listed[mine.Process.Pid] || listed[other.Process.Pid] {
		t.Fatalf("listed %v: want the root %d, not %d", procs, mine.Process.Pid, other.Process.Pid)
	}
}

// omaterm's own processes are never listed, even inside the project:
// killing the app or its daemon would end every session.
func TestListProjectProcessesHidesOmaterm(t *testing.T) {
	wd, _ := os.Getwd()
	procs, _ := ListProjectProcesses(wd, []int{os.Getpid()})
	for _, p := range procs {
		if p.Pid == os.Getpid() {
			t.Fatal("listed the app itself")
		}
	}
	for comm, want := range map[string]bool{"omaterm": true, "omaterm-dev": true, "zsh": false} {
		if got := ownProcess(1, comm); got != want {
			t.Errorf("ownProcess(%q) = %v, want %v", comm, got, want)
		}
	}
}

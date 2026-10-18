package service

import (
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

	procs, err := ListProjectProcesses(dir)
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
	procs2, err := ListProjectProcesses(dir)
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

	procs, _ := ListProjectProcesses(dir)
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

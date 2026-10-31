package service

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestListProjectProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("it starts sleep or python3, Unix programs")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("it starts sleep or python3, Unix programs")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("it starts sleep or python3, Unix programs")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("it starts sleep or python3, Unix programs")
	}
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

func TestParsePSUsage(t *testing.T) {
	got := parsePSUsage("    1   0.0  12288\n 4242  12.5 524288\nbad line\n")
	if got[1] != (usage{"0.0%", "12 MB"}) || got[4242] != (usage{"12.5%", "512 MB"}) || len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestParseLsofListen(t *testing.T) {
	// lsof -nP -a -iTCP -sTCP:LISTEN -p 501,502 -Fpn on macOS: Vite on
	// IPv6 loopback only, a server on two families, one not asked for.
	out := "p501\nf23\nn[::1]:5173\np502\nf5\nn*:3000\nf6\nn127.0.0.1:3000\nf7\nn127.0.0.1:9229\np999\nf3\nn*:80\n"
	ports := parseLsofListen(out, map[int]bool{501: true, 502: true}, map[int]string{501: "node", 502: "ruby"})
	var got []string
	for _, p := range ports {
		got = append(got, fmt.Sprintf("%d:%s:%d", p.Port, p.Process, p.Pid))
	}
	if want := []string{"3000:ruby:502", "5173:node:501", "9229:ruby:502"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Native Claude Code's executable is named by its version; the Info panel
// names it by the command it was run as.
func TestDisplayName(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"2.1.3", []string{"claude", "--resume"}, "claude"},
		{"2.1.3", []string{"/Users/me/.local/bin/claude"}, "claude"},
		{"2.1.3", nil, "2.1.3"},
		{"2.1.3", []string{"/Users/me/.local/share/claude/versions/2.1.3"}, "2.1.3"},
		{"node", []string{"node", "server.js"}, "node"},
		{"1password", []string{"op"}, "1password"},
	} {
		if got := displayName(c.name, c.args); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// A project is a folder scoped closely enough to be one; a drive's root,
// Windows' own folders and the home folder are not, while D:\app is.
func TestWorkspaceTooBroad(t *testing.T) {
	for _, c := range []struct {
		goos, abs, home string
		want            bool
	}{
		{"linux", "/", "/home/me", true},
		{"linux", "/home", "/home/me", true},
		{"linux", "/tmp", "/home/me", true},
		{"linux", "/fachri", "/home/me", true},
		{"linux", "/home/me", "/home/me", true},
		{"linux", "/home/me/proj", "/home/me", false},
		{"linux", "/srv/app", "/home/me", false},
		{"windows", `C:\`, `C:\Users\me`, true},
		{"windows", `C:\Users`, `C:\Users\me`, true},
		{"windows", `c:\users`, `C:\Users\me`, true},
		{"windows", `C:\Windows`, `C:\Users\me`, true},
		{"windows", `C:\Program Files`, `C:\Users\me`, true},
		{"windows", `C:\Program Files (x86)`, `C:\Users\me`, true},
		{"windows", `C:\Users\me`, `C:\Users\me`, true},
		{"windows", `c:\users\ME`, `C:\Users\me`, true},
		{"windows", `C:\Users\me\src`, `C:\Users\me`, false},
		{"windows", `D:\`, `C:\Users\me`, true},
		{"windows", `D:\app`, `C:\Users\me`, false},
		{"windows", `D:\app\src`, `C:\Users\me`, false},
	} {
		if got := workspaceTooBroad(c.goos, c.abs, c.home); got != c.want {
			t.Errorf("%s %q (home %q) = %v, want %v", c.goos, c.abs, c.home, got, c.want)
		}
	}
}

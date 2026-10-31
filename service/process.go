package service

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"omaterm/internal/pty"
)

// Process is a running process whose working directory is inside the
// project workspace.
type Process struct {
	Name string // comm (process name)
	Pid  int
	CPU  string // "%CPU" from ps
	Mem  string // RSS in MB, e.g. "42 MB"
	Cwd  string // resolved working directory
	Cmd  string // full command line (truncated)
	Root int    // the root (a pane's shell) it is or descends from, 0 for none
}

// Port is a listening TCP port owned by a project process.
type Port struct {
	Port    int
	Name    string // service name from /etc/services, or process name
	Open    bool   // true = listening
	Pid     int
	Process string
}

// ListProjectProcesses returns the project's processes: roots (the shells
// of its panes) and their descendants, and those whose CWD is under
// workspace. omaterm's own processes are never listed: killing one would
// end every session. Processes are read as panes read them (Linux's /proc,
// macOS's libSystem), their CPU and memory from ps.
func ListProjectProcesses(workspace string, roots []int) ([]Process, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, nil
	}
	if IsWorkspaceTooBroad(workspace) {
		return nil, nil
	}
	abs, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		abs, _ = filepath.Abs(workspace)
		if abs == "" {
			abs = workspace
		}
	}
	under := func(dir string) bool {
		return dir == abs || strings.HasPrefix(dir, abs+string(filepath.Separator))
	}

	all := pty.Procs()
	if len(all) == 0 {
		return nil, fmt.Errorf("could not list the system's processes")
	}
	usage := psUsage()

	// The project's panes and what they started, wherever it went.
	pidSet := map[int]bool{}
	rootOf := map[int]int{}
	children := childrenMap(all)
	for _, root := range roots {
		pidSet[root] = true
		if rootOf[root] == 0 {
			rootOf[root] = root
		}
		for _, desc := range descendants(children, root) {
			pidSet[desc] = true
			if rootOf[desc] == 0 {
				rootOf[desc] = root
			}
		}
	}

	var procs []Process
	for _, pid := range all {
		cwd := pty.ProcCwd(pid)
		if cwd == "" {
			continue // gone, or not the user's
		}
		resolvedCwd := cwd
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			resolvedCwd = r
		}

		// In the project: a pane's or what it started, its CWD under the
		// workspace, or the PWD it was started with.
		inProject := pidSet[pid] || under(resolvedCwd)
		if !inProject {
			for _, env := range pty.ProcEnv(pid) {
				if pwd, ok := strings.CutPrefix(env, "PWD="); ok {
					if r, err := filepath.EvalSymlinks(pwd); err == nil {
						pwd = r
					}
					inProject = under(pwd)
					break
				}
			}
		}
		if !inProject {
			continue
		}

		name := pty.ProcName(pid)
		if name == "" {
			name = "?"
		}
		if ownProcess(pid, name) {
			continue
		}
		args := pty.ProcArgs(pid)
		name = displayName(name, args)

		// The full command line, truncated.
		cmdLine := strings.TrimSpace(strings.Join(args, " "))
		if r := []rune(cmdLine); len(r) > 60 {
			cmdLine = string(r[:57]) + "..." // whole characters: cut by bytes, one broke
		}

		u, ok := usage[pid]
		if !ok {
			u.cpu = "0%"
		}
		procs = append(procs, Process{
			Name: name,
			Pid:  pid,
			CPU:  u.cpu,
			Mem:  u.mem,
			Cwd:  resolvedCwd,
			Cmd:  cmdLine,
			Root: rootOf[pid],
		})
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].Pid < procs[j].Pid })
	return procs, nil
}

// usage is a process's CPU and memory, as ps shows them.
type usage struct {
	cpu string // "12.5%"
	mem string // "42 MB"
}

// psUsage reads every process's CPU and resident memory in one ps call,
// whose flags Linux's ps and macOS's take alike (RSS in KiB on both).
func psUsage() map[int]usage {
	ps := exec.Command("ps", "-eo", "pid=,pcpu=,rss=")
	hide(ps)
	out, err := ps.Output()
	if err != nil {
		return nil
	}
	return parsePSUsage(string(out))
}

func parsePSUsage(out string) map[int]usage {
	m := map[int]usage{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		u := usage{cpu: fields[1] + "%"}
		if kb, err := strconv.Atoi(fields[2]); err == nil {
			u.mem = fmt.Sprintf("%d MB", kb/1024)
		}
		m[pid] = u
	}
	return m
}

// displayName is what the Info panel calls a process: its name, unless
// that is a version, as native Claude Code's executable is named
// (~/.local/share/claude/versions/2.1.3), when the command it was run as
// says more: "claude".
func displayName(name string, args []string) string {
	if len(args) == 0 || !isVersion(name) {
		return name
	}
	if base := filepath.Base(args[0]); base != "" && base != "." && !isVersion(base) {
		return base
	}
	return name
}

// isVersion tells whether s is digits and dots, as 2.1.3.
func isVersion(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	return strings.Trim(s, "0123456789.") == ""
}

// ownProcess reports whether a process is omaterm's own: the app, its
// session daemon, or an omaterm command a pane runs.
func ownProcess(pid int, comm string) bool {
	return pid == os.Getpid() || strings.HasPrefix(comm, "omaterm")
}

// childrenMap maps each of pids to its children.
func childrenMap(pids []int) map[int][]int {
	children := map[int][]int{}
	for _, pid := range pids {
		if ppid := pty.ProcParent(pid); ppid > 0 {
			children[ppid] = append(children[ppid], pid)
		}
	}
	return children
}

// descendants returns all descendant PIDs of pid.
func descendants(children map[int][]int, pid int) []int {
	var result []int
	queue := []int{pid}
	visited := map[int]bool{pid: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if !visited[child] {
				visited[child] = true
				result = append(result, child)
				queue = append(queue, child)
			}
		}
	}
	return result
}

// ListProjectPorts returns listening TCP ports owned by the given processes.
func ListProjectPorts(processes []Process) ([]Port, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil
	}
	pidSet := map[int]bool{}
	procName := map[int]string{}
	for _, p := range processes {
		pidSet[p.Pid] = true
		procName[p.Pid] = p.Name
	}
	if runtime.GOOS == "darwin" {
		return listPortsViaLsof(pidSet, procName)
	}

	// Try ss first.
	ports, err := listPortsViaSS(pidSet, procName)
	if err == nil && len(ports) > 0 {
		return ports, nil
	}
	// Fallback: parse /proc/net/tcp + /proc/[pid]/fd/.
	ports, err = listPortsViaProc(pidSet, procName)
	if err != nil {
		return nil, err
	}
	return ports, nil
}

func listPortsViaSS(pidSet map[int]bool, procName map[int]string) ([]Port, error) {
	cmd := exec.Command("ss", "-tlnp")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ports []Port
	seen := map[int]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "LISTEN" {
			continue
		}
		localAddr := fields[3]
		portStr := localAddr
		if idx := strings.LastIndex(localAddr, ":"); idx >= 0 {
			portStr = localAddr[idx+1:]
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		rest := strings.Join(fields[4:], " ")

		// Parse all pid= occurrences (ss can show multiple processes).
		foundPid := 0
		foundName := ""
		for {
			idx := strings.Index(rest, "pid=")
			if idx < 0 {
				break
			}
			pidStr := rest[idx+4:]
			if end := strings.IndexAny(pidStr, ",)"); end >= 0 {
				pidStr = pidStr[:end]
			}
			pid, _ := strconv.Atoi(pidStr)
			if pidSet[pid] {
				foundPid = pid
				break
			}
			// advance
			rest = rest[idx+4:]
		}
		// Parse process name.
		if idx := strings.Index(rest, "((\""); idx >= 0 {
			name := rest[idx+3:]
			if end := strings.Index(name, "\""); end >= 0 {
				foundName = name[:end]
			}
		}
		if foundPid == 0 {
			continue
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		if foundName == "" {
			foundName = procName[foundPid]
		}
		if foundName == "" {
			foundName = "unknown"
		}
		ports = append(ports, Port{
			Port:    port,
			Name:    portName(port, foundName),
			Open:    true,
			Pid:     foundPid,
			Process: foundName,
		})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports, nil
}

// listPortsViaLsof lists the ports with lsof, which macOS always has (it
// has neither ss nor /proc/net).
func listPortsViaLsof(pidSet map[int]bool, procName map[int]string) ([]Port, error) {
	if len(pidSet) == 0 {
		return nil, nil
	}
	pids := make([]string, 0, len(pidSet))
	for pid := range pidSet {
		pids = append(pids, strconv.Itoa(pid))
	}
	sort.Strings(pids)
	cmd := exec.Command("lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-p", strings.Join(pids, ","), "-Fpn")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, nil // lsof exits 1 when nothing listens
		}
		return nil, err
	}
	return parseLsofListen(string(out), pidSet, procName), nil
}

// parseLsofListen reads lsof -Fpn: a "p<pid>" line, then a line for each
// of its files, "n<address>:<port>" for a socket (f lines between).
func parseLsofListen(out string, pidSet map[int]bool, procName map[int]string) []Port {
	var ports []Port
	seen := map[int]bool{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			i := strings.LastIndexByte(line, ':')
			if i < 0 || !pidSet[pid] {
				continue
			}
			port, err := strconv.Atoi(line[i+1:])
			if err != nil || seen[port] {
				continue
			}
			seen[port] = true
			name := procName[pid]
			if name == "" {
				name = "unknown"
			}
			ports = append(ports, Port{Port: port, Name: portName(port, name), Open: true, Pid: pid, Process: name})
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports
}

func listPortsViaProc(pidSet map[int]bool, procName map[int]string) ([]Port, error) {
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return nil, err
	}
	type listenInfo struct {
		port  int
		inode string
	}
	var listeners []listenInfo
	parseTCP := func(raw string) {
		for _, line := range strings.Split(raw, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}
			if fields[3] != "0A" {
				continue
			}
			local := fields[1]
			parts := strings.Split(local, ":")
			if len(parts) != 2 {
				continue
			}
			port, err := strconv.ParseInt(parts[1], 16, 32)
			if err != nil {
				continue
			}
			listeners = append(listeners, listenInfo{port: int(port), inode: fields[9]})
		}
	}
	parseTCP(string(data))
	if data6, err := os.ReadFile("/proc/net/tcp6"); err == nil {
		parseTCP(string(data6))
	}

	inodeToPid := map[string]int{}
	for pid := range pidSet {
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
				inode := link[8 : len(link)-1]
				inodeToPid[inode] = pid
			}
		}
	}

	var ports []Port
	seen := map[int]bool{}
	for _, l := range listeners {
		pid, ok := inodeToPid[l.inode]
		if !ok || !pidSet[pid] {
			continue
		}
		if seen[l.port] {
			continue
		}
		seen[l.port] = true
		name := procName[pid]
		if name == "" {
			name = "unknown"
		}
		ports = append(ports, Port{
			Port:    l.port,
			Name:    portName(l.port, name),
			Open:    true,
			Pid:     pid,
			Process: name,
		})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports, nil
}

// KillProcess sends SIGTERM to the given PID.
func KillProcess(pid int) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("not supported on this platform")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

// portName is what a port shows as: the process that listens on it, as
// "bun" or "vite" tells a user what their dev server is doing, where
// /etc/services calls 8080 "http-alt". A process omaterm cannot name falls
// back to the service's own name.
func portName(port int, process string) string {
	if process != "" && process != "unknown" {
		return process
	}
	f, err := os.Open("/etc/services")
	if err != nil {
		return process
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		portProto := strings.Split(fields[1], "/")
		if len(portProto) != 2 {
			continue
		}
		p, err := strconv.Atoi(portProto[0])
		if err != nil || p != port {
			continue
		}
		return fields[0]
	}
	return process
}

// IsWorkspaceTooBroad reports whether the workspace path is so broad that
// scoping would include too many unrelated processes.
func IsWorkspaceTooBroad(workspace string) bool {
	// Expand ~.
	if strings.HasPrefix(workspace, "~") {
		home, _ := os.UserHomeDir()
		if len(workspace) == 1 {
			workspace = home
		} else if len(workspace) > 1 && workspace[1] == '/' {
			workspace = filepath.Join(home, workspace[2:])
		}
	}
	abs, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		abs, _ = filepath.Abs(workspace)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if h, _ := filepath.EvalSymlinks(home); h != "" {
			home = h
		}
	}
	broad := []string{"/", "/home", "/usr", "/tmp", "/var", "/opt", "/etc"}
	for _, b := range broad {
		if abs == b {
			return true
		}
	}
	if abs == home {
		return true
	}
	// Only one path component under root is too broad.
	if strings.Count(strings.TrimPrefix(abs, "/"), "/") == 0 && abs != "/" {
		// e.g. /home, /tmp (already covered) but also /fachri if weird
		return true
	}
	return false
}

package service

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
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
}

// Port is a listening TCP port owned by a project process.
type Port struct {
	Port    int
	Name    string // service name from /etc/services, or process name
	Open    bool   // true = listening
	Pid     int
	Process string
}

// ListProjectProcesses returns processes whose CWD is under workspace, or
// that are descendants of the omaterm process itself (terminal shells and
// their children).
func ListProjectProcesses(workspace string) ([]Process, error) {
	if runtime.GOOS != "linux" {
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

	// CPU% from ps (one call for all processes).
	cpuMap := map[int]string{}
	if out, err := exec.Command("ps", "-eo", "pid=,pcpu=").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil {
				continue
			}
			cpuMap[pid] = fields[1] + "%"
		}
	}

	// Build a set of project PIDs: descendants of the omaterm process itself.
	// Terminal shells and their children are all descendants of omaterm.
	pidSet := map[int]bool{}
	for _, desc := range descendantsOf(os.Getpid()) {
		pidSet[desc] = true
	}
	pidSet[os.Getpid()] = true

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var procs []Process
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue
		}
		resolvedCwd := cwd
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			resolvedCwd = r
		}

		// Check if process is in project: CWD under workspace OR descendant of terminal.
		inProject := pidSet[pid]
		if !inProject {
			// CWD under workspace.
			if resolvedCwd == abs || strings.HasPrefix(resolvedCwd, abs+string(filepath.Separator)) {
				inProject = true
			}
		}
		if !inProject {
			// Environment-based: check if PWD or PROJECT_ROOT matches.
			if envData, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ")); err == nil {
				for _, env := range strings.Split(string(envData), "\x00") {
					if strings.HasPrefix(env, "PWD=") {
						pwd := strings.TrimPrefix(env, "PWD=")
						if resolved, err := filepath.EvalSymlinks(pwd); err == nil {
							pwd = resolved
						}
						if pwd == abs || strings.HasPrefix(pwd, abs+string(filepath.Separator)) {
							inProject = true
						}
					}
				}
			}
		}
		if !inProject {
			continue
		}

		// Read process name.
		name := ""
		if data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm")); err == nil {
			name = strings.TrimSpace(string(data))
		}
		if name == "" {
			name = "?"
		}

		// Read full command line (truncated).
		cmdLine := ""
		if data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline")); err == nil {
			cmdLine = strings.ReplaceAll(string(data), "\x00", " ")
			cmdLine = strings.TrimSpace(cmdLine)
			if len(cmdLine) > 60 {
				cmdLine = cmdLine[:57] + "..."
			}
		}

		// Read RSS from statm.
		memStr := ""
		if data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "statm")); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				if rss, err := strconv.Atoi(fields[1]); err == nil {
					mb := rss * 4096 / 1024 / 1024
					memStr = fmt.Sprintf("%d MB", mb)
				}
			}
		}
		cpuStr := cpuMap[pid]
		if cpuStr == "" {
			cpuStr = "0%"
		}

		procs = append(procs, Process{
			Name: name,
			Pid:  pid,
			CPU:  cpuStr,
			Mem:  memStr,
			Cwd:  resolvedCwd,
			Cmd:  cmdLine,
		})
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].Pid < procs[j].Pid })
	return procs, nil
}

// descendantsOf returns all descendant PIDs of the given PID.
func descendantsOf(pid int) []int {
	// Build a map of ppid -> children.
	children := map[int][]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		childPid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// stat format: pid (comm) state ppid ...
		// comm may contain spaces/parens, so find the last ")".
		s := string(data)
		idx := strings.LastIndex(s, ")")
		if idx < 0 || idx+2 >= len(s) {
			continue
		}
		fields := strings.Fields(s[idx+2:])
		if len(fields) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		children[ppid] = append(children[ppid], childPid)
	}

	// BFS from pid.
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
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	pidSet := map[int]bool{}
	procName := map[int]string{}
	for _, p := range processes {
		pidSet[p.Pid] = true
		procName[p.Pid] = p.Name
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
	out, err := exec.Command("ss", "-tlnp").Output()
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
			Name:    resolvePortName(port, foundName),
			Open:    true,
			Pid:     foundPid,
			Process: foundName,
		})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports, nil
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
			Name:    resolvePortName(l.port, name),
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
	if runtime.GOOS != "linux" {
		return fmt.Errorf("not supported on this platform")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

// resolvePortName looks up the port in /etc/services, falling back to
// the process name.
func resolvePortName(port int, fallback string) string {
	f, err := os.Open("/etc/services")
	if err != nil {
		return fallback
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
	return fallback
}

// ListProjectProcesses signature changed: now accepts terminal PIDs.
// TerminalPids returns the PIDs of shells spawned by omaterm.
func TerminalPids() []int {
	return nil // placeholder; populated by app state
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

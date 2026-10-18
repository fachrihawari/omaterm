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
}

// Port is a listening TCP port owned by a project process.
type Port struct {
	Port    int
	Name    string // service name from /etc/services, or process name
	Open    bool   // true = listening
	Pid     int
	Process string
}

// ListProjectProcesses returns processes whose CWD is under workspace.
// On non-Linux platforms it returns an empty list.
func ListProjectProcesses(workspace string) ([]Process, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}

	// CPU% from ps (one call for all processes).
	cpuMap := map[int]string{}
	memMap := map[int]string{}
	if out, err := exec.Command("ps", "-eo", "pid=,pcpu=,pmem=").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil {
				continue
			}
			cpuMap[pid] = fields[1] + "%"
			memMap[pid] = fields[2] + "%"
		}
	}

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
			continue // permission denied or process gone
		}
		// Check if cwd is under workspace.
		if cwd != abs && !strings.HasPrefix(cwd, abs+string(filepath.Separator)) {
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
		// Read RSS from statm (pages * 4096).
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
		if memStr == "" {
			memStr = memMap[pid]
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
			Cwd:  cwd,
		})
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].Pid < procs[j].Pid })
	return procs, nil
}

// ListProjectPorts returns listening TCP ports owned by the given processes.
func ListProjectPorts(processes []Process) ([]Port, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	pidSet := map[int]bool{}
	for _, p := range processes {
		pidSet[p.Pid] = true
	}

	// Try ss first.
	ports, err := listPortsViaSS(pidSet)
	if err == nil {
		return ports, nil
	}
	// Fallback: parse /proc/net/tcp + /proc/[pid]/fd/.
	return listPortsViaProc(pidSet, processes)
}

func listPortsViaSS(pidSet map[int]bool) ([]Port, error) {
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
		// Local Address:Port is field[3]
		localAddr := fields[3]
		portStr := localAddr
		if idx := strings.LastIndex(localAddr, ":"); idx >= 0 {
			portStr = localAddr[idx+1:]
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		// Parse process info from the rest of the line.
		rest := strings.Join(fields[5:], " ")
		pid := 0
		procName := ""
		if idx := strings.Index(rest, "pid="); idx >= 0 {
			pidStr := rest[idx+4:]
			if end := strings.IndexAny(pidStr, ",)"); end >= 0 {
				pidStr = pidStr[:end]
			}
			pid, _ = strconv.Atoi(pidStr)
		}
		if idx := strings.Index(rest, "((\""); idx >= 0 {
			name := rest[idx+3:]
			if end := strings.Index(name, "\""); end >= 0 {
				procName = name[:end]
			}
		}
		if !pidSet[pid] {
			continue
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		if procName == "" {
			procName = "unknown"
		}
		ports = append(ports, Port{
			Port:    port,
			Name:    resolvePortName(port, procName),
			Open:    true,
			Pid:     pid,
			Process: procName,
		})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports, nil
}

func listPortsViaProc(pidSet map[int]bool, processes []Process) ([]Port, error) {
	// Parse /proc/net/tcp for listening sockets (state 0A = LISTEN).
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return nil, err
	}
	// Collect inode -> port mapping.
	type listenInfo struct {
		port  int
		inode string
	}
	var listeners []listenInfo
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[3] != "0A" { // LISTEN
			continue
		}
		// local_address is field[1], format IP:PORT (hex)
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
	// Also read tcp6.
	if data6, err := os.ReadFile("/proc/net/tcp6"); err == nil {
		for _, line := range strings.Split(string(data6), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
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

	// Map inode -> pid by scanning /proc/[pid]/fd/.
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
			// link format: socket:[inode]
			if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") {
				inode := link[8 : len(link)-1]
				inodeToPid[inode] = pid
			}
		}
	}

	// Build process name map.
	procName := map[int]string{}
	for _, p := range processes {
		procName[p.Pid] = p.Name
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
		// fields[1] = "port/proto"
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

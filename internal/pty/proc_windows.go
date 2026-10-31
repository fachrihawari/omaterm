package pty

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Process lookups on Windows: names and parents from a snapshot of the
// process list (Toolhelp32), shared for a moment by every pane that asks;
// arguments from the process's command line. A shell's directory isn't
// read: PowerShell doesn't move its process's when it changes its own, so
// panes follow the terminal's (OSC 7) instead.

var (
	ntdll                         = syscall.NewLazyDLL("ntdll.dll") // a KnownDLL
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
)

var snap struct {
	sync.Mutex
	at    time.Time
	procs []winProc
}

// snapshot lists the processes, as they were at most 300ms ago.
func snapshot() []winProc {
	snap.Lock()
	defer snap.Unlock()
	if time.Since(snap.at) < 300*time.Millisecond {
		return snap.procs
	}
	h, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(h)
	var procs []winProc
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(h, &e); err == nil; err = syscall.Process32Next(h, &e) {
		procs = append(procs, winProc{pid: int(e.ProcessID), ppid: int(e.ParentProcessID), name: exeName(syscall.UTF16ToString(e.ExeFile[:]))})
	}
	snap.at, snap.procs = time.Now(), procs
	return procs
}

func lookup(pid int) (winProc, bool) {
	for _, p := range snapshot() {
		if p.pid == pid {
			return p, true
		}
	}
	return winProc{}, false
}

// ProcName is a process's executable name without .exe: "node".
func ProcName(pid int) string {
	p, _ := lookup(pid)
	return p.name
}

// ProcParent is a process's parent, 0 when unknown.
func ProcParent(pid int) int {
	p, _ := lookup(pid)
	return p.ppid
}

// Procs lists every process.
func Procs() []int {
	all := snapshot()
	pids := make([]int, len(all))
	for i, p := range all {
		pids[i] = p.pid
	}
	return pids
}

// ProcArgs is a process's command line, split as the C runtime splits it;
// nil when it can't be read.
func ProcArgs(pid int) []string {
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(h)
	// ProcessCommandLineInformation (Windows 8.1 and later) gives a
	// UNICODE_STRING followed by the text it points at.
	const processCommandLineInformation = 60
	buf := make([]byte, 1024)
	for {
		var n uint32
		r, _, _ := procNtQueryInformationProcess.Call(uintptr(h), processCommandLineInformation, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)))
		if r == 0 {
			break
		}
		const statusInfoLengthMismatch = 0xC0000004
		if uint32(r) != statusInfoLengthMismatch || int(n) <= len(buf) || n > 1<<20 {
			return nil
		}
		buf = make([]byte, n)
	}
	us := (*struct {
		length, maximumLength uint16
		buffer                *uint16
	})(unsafe.Pointer(&buf[0]))
	if us.buffer == nil || us.length == 0 {
		return nil
	}
	line := unsafe.Slice(us.buffer, us.length/2)
	return splitCommandLine(syscall.UTF16ToString(line))
}

// ProcEnv isn't read on Windows.
func ProcEnv(pid int) []string { return nil }

// ProcCwd isn't read on Windows (see above).
func ProcCwd(pid int) string { return "" }

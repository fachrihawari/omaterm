package service

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

// Windows' counterparts of ps and ss: memory from each process's counters,
// listening ports from the TCP tables, which name their owning process.

var (
	iphlpapi                    = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable     = iphlpapi.NewProc("GetExtendedTcpTable")
	kernel32                    = syscall.NewLazyDLL("kernel32.dll") // a KnownDLL
	procK32GetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	procGetSystemTimes          = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx    = kernel32.NewProc("GlobalMemoryStatusEx")
)

// processUsage is each process's memory, its working set; Windows keeps no
// CPU share to read in one look, so CPU is left out.
func processUsage(pids []int) map[int]usage {
	const processQueryLimitedInformation = 0x1000
	m := map[int]usage{}
	for _, pid := range pids {
		h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
		if err != nil {
			continue
		}
		var c struct {
			cb, pageFaultCount                   uint32
			peakWorkingSetSize, workingSetSize   uintptr
			quotaPeakPagedPoolUsage, quotaPaged  uintptr
			quotaPeakNonPagedPoolUsage, quotaNon uintptr
			pagefileUsage, peakPagefileUsage     uintptr
		}
		c.cb = uint32(unsafe.Sizeof(c))
		if r, _, _ := procK32GetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); r != 0 {
			m[pid] = usage{mem: fmt.Sprintf("%d MB", c.workingSetSize>>20)}
		}
		syscall.CloseHandle(h)
	}
	return m
}

// listPortsWindows lists the ports the processes listen on, IPv4 and IPv6.
func listPortsWindows(pidSet map[int]bool, procName map[int]string) ([]Port, error) {
	const afInet, afInet6, tcpTableOwnerPIDListener = 2, 23, 3
	var listeners []tcpListener
	for _, af := range []int{afInet, afInet6} {
		var size uint32
		procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
		if size == 0 {
			continue
		}
		buf := make([]byte, size+1024) // the table may grow in between
		size = uint32(len(buf))
		if r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0); r != 0 {
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", syscall.Errno(r))
		}
		listeners = append(listeners, parseTCPTable(buf[:size], af == afInet6)...)
	}
	return portsOf(listeners, pidSet, procName), nil
}

// cpuTimes is GetSystemTimes': the kernel's time includes its idle time.
func cpuTimes() (total, idle uint64, ok bool) {
	var i, k, u syscall.Filetime
	if r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))); r == 0 {
		return 0, 0, false
	}
	ft := func(f syscall.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	return ft(k) + ft(u), ft(i), true
}

// memUsage is GlobalMemoryStatusEx's: what isn't available is used.
func memUsage() (used, total uint64, ok bool) {
	var b [64]byte // MEMORYSTATUSEX
	binary.LittleEndian.PutUint32(b[0:], uint32(len(b)))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&b[0]))); r == 0 {
		return 0, 0, false
	}
	total = binary.LittleEndian.Uint64(b[8:])
	avail := binary.LittleEndian.Uint64(b[16:])
	return total - avail, total, total > 0
}

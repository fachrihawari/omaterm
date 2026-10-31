package service

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS has no /proc: the CPU's ticks and the memory's pages come from
// mach's host_statistics, and the memory's size from the hw.memsize
// sysctl, called through purego (no cgo).

var (
	machHostSelf     func() uint32
	hostStatistics   func(host uint32, flavor int32, info unsafe.Pointer, count unsafe.Pointer) int32
	hostStatistics64 func(host uint32, flavor int32, info unsafe.Pointer, count unsafe.Pointer) int32
	hostPageSize     func(host uint32, size unsafe.Pointer) int32
	sysctlbyname     func(name string, old unsafe.Pointer, oldlen unsafe.Pointer, new unsafe.Pointer, newlen uintptr) int32
)

const (
	hostCPULoadInfo      = 3 // HOST_CPU_LOAD_INFO: struct host_cpu_load_info
	hostCPULoadInfoCount = 4 // its natural_t: cpu_ticks[CPU_STATE_MAX]
	hostVMInfo64         = 4 // HOST_VM_INFO64: struct vm_statistics64
	hostVMInfo64Count    = vmStatsSize / 4
)

// machHost is the host port, taken once: each mach_host_self call adds a
// reference to it.
var machHost = sync.OnceValue(func() uint32 {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	purego.RegisterLibFunc(&machHostSelf, lib, "mach_host_self")
	purego.RegisterLibFunc(&hostStatistics, lib, "host_statistics")
	purego.RegisterLibFunc(&hostStatistics64, lib, "host_statistics64")
	purego.RegisterLibFunc(&hostPageSize, lib, "host_page_size")
	purego.RegisterLibFunc(&sysctlbyname, lib, "sysctlbyname")
	return machHostSelf()
})

var (
	ticksMu sync.Mutex
	ticks   machTicks
)

// cpuTimes returns the host's ticks so far, and the idle ones.
func cpuTimes() (total, idle uint64, ok bool) {
	host := machHost()
	if host == 0 {
		return 0, 0, false
	}
	var t [hostCPULoadInfoCount]uint32
	count := uint32(hostCPULoadInfoCount)
	if hostStatistics(host, hostCPULoadInfo, unsafe.Pointer(&t[0]), unsafe.Pointer(&count)) != 0 || count != hostCPULoadInfoCount {
		return 0, 0, false
	}
	ticksMu.Lock()
	defer ticksMu.Unlock()
	total, idle = ticks.add(t)
	return total, idle, true
}

// memUsage returns the memory in use, as Activity Monitor counts it, and
// all of it, in bytes.
func memUsage() (used, total uint64, ok bool) {
	host := machHost()
	if host == 0 {
		return 0, 0, false
	}
	size := uintptr(8)
	if sysctlbyname("hw.memsize", unsafe.Pointer(&total), unsafe.Pointer(&size), nil, 0) != 0 || total == 0 {
		return 0, 0, false
	}
	// The statistics count pages of the kernel's size (16 KiB on Apple
	// silicon), which host_page_size gives.
	var page uintptr
	if hostPageSize(host, unsafe.Pointer(&page)) != 0 {
		return 0, 0, false
	}
	vm := make([]byte, vmStatsSize)
	count := uint32(hostVMInfo64Count)
	if hostStatistics64(host, hostVMInfo64, unsafe.Pointer(&vm[0]), unsafe.Pointer(&count)) != 0 {
		return 0, 0, false
	}
	used, ok = machMemUsed(vm, uint64(page))
	if !ok {
		return 0, 0, false
	}
	return min(used, total), total, true
}

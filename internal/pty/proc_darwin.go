package pty

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS has no /proc: a process is read through libSystem's proc_pidinfo
// and the kern.procargs2 sysctl, as ps does, called through purego as the
// UI's own darwin code is (no cgo).

var (
	procPidinfo     func(pid, flavor int32, arg uint64, buf unsafe.Pointer, size int32) int32
	procListAllPids func(buf unsafe.Pointer, size int32) int32
	sysctl          func(name unsafe.Pointer, namelen uint32, old unsafe.Pointer, oldlen unsafe.Pointer, new unsafe.Pointer, newlen uintptr) int32
)

// libSystem loads the functions once, reporting whether it could.
var libSystem = sync.OnceValue(func() bool {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return false
	}
	purego.RegisterLibFunc(&procPidinfo, lib, "proc_pidinfo")
	purego.RegisterLibFunc(&procListAllPids, lib, "proc_listallpids")
	purego.RegisterLibFunc(&sysctl, lib, "sysctl")
	return true
})

const (
	procPidTBSDInfo       = 3    // PROC_PIDTBSDINFO: struct proc_bsdinfo
	procPidTBSDInfoSize   = 136  // its size
	procPidVnodePathInfo  = 9    // PROC_PIDVNODEPATHINFO: struct proc_vnodepathinfo
	procPidVnodePathSize  = 2352 // its size: two vnode_info_path
	vnodeInfoSize         = 152  // struct vnode_info, before the cwd's path
	maxPathLen            = 1024
	ctlKern, kernProcArgs = 1, 49 // CTL_KERN, KERN_PROCARGS2
)

// bsdInfo returns pid's struct proc_bsdinfo, nil when it is gone.
func bsdInfo(pid int) []byte {
	if pid <= 0 || !libSystem() {
		return nil
	}
	b := make([]byte, procPidTBSDInfoSize)
	if procPidinfo(int32(pid), procPidTBSDInfo, 0, unsafe.Pointer(&b[0]), int32(len(b))) != int32(len(b)) {
		return nil
	}
	return b
}

// cString is b up to its first NUL.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// ProcName returns a process's name, as ps shows it: pbi_name, which
// holds up to 32 bytes, else the 16 of pbi_comm.
func ProcName(pid int) string {
	b := bsdInfo(pid)
	if b == nil {
		return ""
	}
	if name := cString(b[64:96]); name != "" {
		return name
	}
	return cString(b[48:64])
}

// procArgs2 returns kern.procargs2 for pid: what it was started with and
// its environment, readable for the user's own processes.
func procArgs2(pid int) []byte {
	if pid <= 0 || !libSystem() {
		return nil
	}
	mib := [3]int32{ctlKern, kernProcArgs, int32(pid)}
	var size uintptr
	if sysctl(unsafe.Pointer(&mib[0]), 3, nil, unsafe.Pointer(&size), nil, 0) != 0 || size == 0 {
		return nil
	}
	b := make([]byte, size)
	if sysctl(unsafe.Pointer(&mib[0]), 3, unsafe.Pointer(&b[0]), unsafe.Pointer(&size), nil, 0) != 0 {
		return nil
	}
	return b[:size]
}

// ProcArgs returns the arguments a process was started with.
func ProcArgs(pid int) []string {
	args, _ := parseProcArgs(procArgs2(pid), binary.LittleEndian)
	return args
}

// ProcEnv returns a process's environment, as it started.
func ProcEnv(pid int) []string {
	_, env := parseProcArgs(procArgs2(pid), binary.LittleEndian)
	return env
}

// ProcCwd returns a process's working directory: the path of pvi_cdir,
// past its vnode_info.
func ProcCwd(pid int) string {
	if pid <= 0 || !libSystem() {
		return ""
	}
	b := make([]byte, procPidVnodePathSize)
	if procPidinfo(int32(pid), procPidVnodePathInfo, 0, unsafe.Pointer(&b[0]), int32(len(b))) != int32(len(b)) {
		return ""
	}
	return cString(b[vnodeInfoSize : vnodeInfoSize+maxPathLen])
}

// ProcParent returns a process's parent, 0 when it is gone: pbi_ppid.
func ProcParent(pid int) int {
	b := bsdInfo(pid)
	if b == nil {
		return 0
	}
	return int(binary.LittleEndian.Uint32(b[16:20]))
}

// Procs returns the processes running now.
func Procs() []int {
	if !libSystem() {
		return nil
	}
	n := procListAllPids(nil, 0)
	if n <= 0 {
		return nil
	}
	buf := make([]int32, n+64) // room for those started meanwhile
	n = procListAllPids(unsafe.Pointer(&buf[0]), int32(4*len(buf)))
	pids := make([]int, 0, max(n, 0))
	for _, pid := range buf[:max(min(int(n), len(buf)), 0)] {
		if pid > 0 {
			pids = append(pids, int(pid))
		}
	}
	return pids
}

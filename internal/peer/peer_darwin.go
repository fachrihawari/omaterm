package peer

import (
	"net"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS has no SO_PEERCRED: libSystem's getpeereid tells the user at the
// other end, called through purego as the rest of the darwin code is (no
// cgo).

var getpeereid func(fd int32, euid, egid unsafe.Pointer) int32

// libSystem loads getpeereid once, reporting whether it could.
var libSystem = sync.OnceValue(func() bool {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return false
	}
	purego.RegisterLibFunc(&getpeereid, lib, "getpeereid")
	return true
})

// UID returns the user of the process at the other end of a Unix socket
// (getpeereid), or -1.
func UID(c net.Conn) int {
	uc, ok := c.(*net.UnixConn)
	if !ok || !libSystem() {
		return -1
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1
	}
	uid := -1
	raw.Control(func(fd uintptr) {
		var euid, egid uint32
		if getpeereid(int32(fd), unsafe.Pointer(&euid), unsafe.Pointer(&egid)) == 0 {
			uid = int(euid)
		}
	})
	return uid
}

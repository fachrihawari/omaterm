// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/peer_linux.go.

// Package peer tells who is at the other end of a Unix socket.
package peer

import (
	"net"
	"syscall"
)

// UID returns the user of the process at the other end of a Unix
// socket (SO_PEERCRED), or -1.
func UID(c net.Conn) int {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1
	}
	uid := -1
	raw.Control(func(fd uintptr) {
		if cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED); err == nil {
			uid = int(cred.Uid)
		}
	})
	return uid
}

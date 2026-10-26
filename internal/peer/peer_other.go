//go:build !linux

package peer

import "net"

// UID is unknown off Linux; the socket's mode keeps others out.
func UID(net.Conn) int { return -1 }

//go:build !linux && !darwin

package peer

import "net"

// UID is unknown here; the socket's mode keeps others out.
func UID(net.Conn) int { return -1 }

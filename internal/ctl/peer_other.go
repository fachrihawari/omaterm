//go:build !linux

package ctl

import "net"

// peerUID is unknown off Linux; the socket's mode keeps others out.
func peerUID(net.Conn) int { return -1 }

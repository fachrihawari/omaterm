//go:build linux || darwin

package peer

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Both ends of a socket of our own are ours. The directory's name is short:
// macOS caps a socket's path at 104 bytes, and its temporary directory is
// long.
func TestUIDIsOurs(t *testing.T) {
	dir, err := os.MkdirTemp("", "omx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, ok := <-accepted
	if !ok {
		t.Fatal("no connection accepted")
	}
	defer s.Close()
	if got := UID(c); got != os.Getuid() {
		t.Errorf("dialer sees the listener as uid %d, want %d", got, os.Getuid())
	}
	if got := UID(s); got != os.Getuid() {
		t.Errorf("listener sees the dialer as uid %d, want %d", got, os.Getuid())
	}
}

// A connection that is not a Unix socket has no peer user.
func TestUIDNotUnix(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := UID(a); got != -1 {
		t.Errorf("UID(pipe) = %d, want -1", got)
	}
}

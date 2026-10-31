//go:build darwin

package devurl

import (
	"io"
	"net"
	"testing"
	"time"
)

// On a real Mac the proxy's port is bound as the daemon binds it. What the
// machine allows differs (root, another program on 443), so it is logged;
// what is asserted holds either way: what was bound serves this machine, and
// the wildcard fallback turns away a dial to another of its addresses.
func TestListenTCPOnThisMac(t *testing.T) {
	ln, err := listenTCP("darwin", "127.0.0.1:443", net.Listen)
	if err != nil {
		t.Logf("port 443 can't be bound here: %v", err)
		if c := (Env{OS: "darwin", Listen: net.Listen}).portCheck(ProxyState{}); c.OK {
			t.Errorf("the port check says OK, though binding failed: %+v", c)
		}
		return
	}
	defer ln.Close()
	_, wild := ln.(loopbackListener)
	t.Logf("bound %s (wildcard fallback: %v)", ln.Addr(), wild)
	if a, ok := ln.Addr().(*net.TCPAddr); !ok || a.Port != 443 {
		t.Fatalf("bound %v, not port 443", ln.Addr())
	}

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- c
		}
	}()

	c, err := net.DialTimeout("tcp", "127.0.0.1:443", 2*time.Second)
	if err != nil {
		t.Fatalf("dialing 127.0.0.1:443: %v", err)
	}
	c.Close()
	select {
	case s := <-accepted:
		s.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("a dial from this machine wasn't accepted")
	}

	if !wild {
		return
	}
	other := nonLoopbackIP()
	if other == nil {
		t.Log("no other address to dial from")
		return
	}
	c, err = net.DialTimeout("tcp", net.JoinHostPort(other.String(), "443"), 2*time.Second)
	if err != nil {
		t.Logf("dialing %s: %v", other, err)
		return
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	// Closed is EOF, or a reset: still open is the read timing out.
	_, err = c.Read(make([]byte, 1))
	if ne, ok := err.(net.Error); err == nil || ok && ne.Timeout() {
		t.Errorf("a dial to %s wasn't closed: %v", other, err)
	} else if err != io.EOF {
		t.Logf("a dial to %s ended with %v", other, err)
	}
	select {
	case s, ok := <-accepted:
		if ok {
			s.Close()
			t.Errorf("a dial to %s was handed out, from %s", other, s.RemoteAddr())
		}
	default:
	}
}

// nonLoopbackIP is one of this machine's IPv4 addresses that isn't
// loopback: dialing it comes from that address, not from 127.0.0.1.
func nonLoopbackIP() net.IP {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
			return n.IP
		}
	}
	return nil
}

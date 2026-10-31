package devurl

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

// fakeAddrConn is a connection that says it comes from addr.
type fakeAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c fakeAddrConn) RemoteAddr() net.Addr { return c.addr }

// fakeListener hands out its connections, then fails.
type fakeListener struct {
	net.Listener
	conns []net.Conn
}

func (l *fakeListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, net.ErrClosed
	}
	c := l.conns[0]
	l.conns = l.conns[1:]
	return c, nil
}

func (l *fakeListener) Close() error { return nil }

// Only connections from this machine come through; the others are closed.
func TestLoopbackOnlyTurnsAwayOthers(t *testing.T) {
	closed := make(chan string, 8)
	conn := func(ip string) net.Conn {
		a, b := net.Pipe()
		t.Cleanup(func() { b.Close() })
		go func() {
			buf := make([]byte, 1)
			if _, err := b.Read(buf); err != nil {
				closed <- ip
			}
		}()
		return fakeAddrConn{a, &net.TCPAddr{IP: net.ParseIP(ip), Port: 50000}}
	}
	l := LoopbackOnly(&fakeListener{conns: []net.Conn{
		conn("192.168.1.20"), conn("127.0.0.1"), conn("fe80::1"), conn("::1"), conn("::ffff:127.0.0.2"), conn("10.0.0.1"),
	}})
	var got []string
	for {
		c, err := l.Accept()
		if err != nil {
			break
		}
		got = append(got, c.RemoteAddr().(*net.TCPAddr).IP.String())
	}
	want := []string{"127.0.0.1", "::1", "127.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("accepted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("accepted %v, want %v", got, want)
		}
	}
	for i := 0; i < 3; i++ {
		select {
		case ip := <-closed:
			if ip != "192.168.1.20" && ip != "fe80::1" && ip != "10.0.0.1" {
				t.Errorf("closed %s", ip)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of the three from elsewhere closed", i)
		}
	}
}

// A real listener on a free port, wrapped, serves a dial from loopback.
func TestLoopbackOnlyServesLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := LoopbackOnly(ln)
	defer l.Close()
	go func() {
		if c, err := net.Dial("tcp", ln.Addr().String()); err == nil {
			c.Write([]byte("x"))
			c.Close()
		}
	}()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err != nil || buf[0] != 'x' {
		t.Fatalf("read %q, %v", buf, err)
	}
}

// listenTCP falls back to every address, turning away others, only on macOS
// and only when the loopback address's port is refused for permission.
func TestListenTCPFallsBackOnMacOS(t *testing.T) {
	eacces := &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EACCES}
	var asked []string
	listen := func(_, addr string) (net.Listener, error) {
		asked = append(asked, addr)
		if addr == "127.0.0.1:443" {
			return nil, eacces
		}
		return net.Listen("tcp", "127.0.0.1:0")
	}

	ln, err := listenTCP("darwin", "127.0.0.1:443", listen)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if _, ok := ln.(loopbackListener); !ok || len(asked) != 2 || asked[1] != ":443" {
		t.Fatalf("listener %T after %v", ln, asked)
	}

	asked = nil
	if _, err := listenTCP("linux", "127.0.0.1:443", listen); !errors.Is(err, syscall.EACCES) || len(asked) != 1 {
		t.Fatalf("linux: %v after %v", err, asked)
	}
	// Another address, or another failure, isn't worked around.
	asked = nil
	if _, err := listenTCP("darwin", "192.168.1.2:443", func(_, addr string) (net.Listener, error) {
		asked = append(asked, addr)
		return nil, eacces
	}); err == nil || len(asked) != 1 {
		t.Fatalf("not loopback: %v after %v", err, asked)
	}
	inUse := &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE}
	asked = nil
	if _, err := listenTCP("darwin", "127.0.0.1:443", func(_, addr string) (net.Listener, error) {
		asked = append(asked, addr)
		return nil, inUse
	}); !errors.Is(err, syscall.EADDRINUSE) || len(asked) != 1 {
		t.Fatalf("in use: %v after %v", err, asked)
	}
	// Both refused: the error tells both.
	if _, err := listenTCP("darwin", "127.0.0.1:443", func(string, string) (net.Listener, error) { return nil, eacces }); err == nil || !errors.Is(err, syscall.EACCES) {
		t.Fatalf("both refused: %v", err)
	}
}

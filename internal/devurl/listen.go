package devurl

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
)

// listenTCP binds addr for the proxy. Since 10.14, macOS lets a user bind a
// port below 1024 only on the wildcard address, so when a loopback address's
// port is refused there, the proxy listens on every address and turns away
// connections from elsewhere. With the firewall on, macOS may ask once
// whether omaterm may accept incoming connections: nothing from elsewhere
// gets through either way. listen is net.Listen but in tests.
func listenTCP(goos, addr string, listen func(network, addr string) (net.Listener, error)) (net.Listener, error) {
	ln, err := listen("tcp", addr)
	if err == nil || goos != "darwin" || !errors.Is(err, fs.ErrPermission) {
		return ln, err
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); splitErr != nil || ip == nil || !ip.IsLoopback() {
		return nil, err
	}
	wild, wildErr := listen("tcp", ":"+port)
	if wildErr != nil {
		return nil, fmt.Errorf("%w; on all addresses: %w", err, wildErr)
	}
	return LoopbackOnly(wild), nil
}

// LoopbackOnly hands out the connections of ln that come from this machine
// (127.0.0.0/8 or ::1), and closes the others as they come.
func LoopbackOnly(ln net.Listener) net.Listener { return loopbackListener{ln} }

type loopbackListener struct{ net.Listener }

func (l loopbackListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if a, ok := c.RemoteAddr().(*net.TCPAddr); ok && a.IP.IsLoopback() {
			return c, nil
		}
		c.Close()
	}
}

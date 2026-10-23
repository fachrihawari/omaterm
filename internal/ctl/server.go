// Package ctl serves omaterm's control socket: a Unix socket only its user
// can reach, where the omaterm command and agents' hooks call the Host
// (the app, or a session daemon) and subscribers hear of every change.
//
// Its checks follow Kero's kerod (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE): the socket made under a umask, so it is
// never anyone else's for a moment, and every connection's user checked.
package ctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"omaterm/internal/proto"
)

// Host answers calls.
type Host interface {
	// Handle runs a method. A *proto.Error's code reaches the caller.
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
	// Subscribe calls fn with events (the state as it changes) until
	// cancelled.
	Subscribe(fn func(proto.Message)) (cancel func())
}

// Attacher is a Host that streams a pane's screen to a connection of
// kind "attach": it writes the welcome, then serves the connection until
// it ends.
type Attacher interface {
	Attach(conn net.Conn, r *bufio.Reader, hello proto.Hello, welcome proto.Welcome)
}

// Server is a listening control socket.
type Server struct {
	ln      net.Listener
	path    string
	host    Host
	app     string
	started time.Time
	wg      sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

// ErrRunning is Listen's error when another omaterm serves the socket.
var ErrRunning = errors.New("ctl: omaterm already serves the socket")

// Listen makes the socket at path, in a directory only its user can read.
func Listen(path string, host Host, app string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if c, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		c.Close()
		return nil, ErrRunning
	}
	os.Remove(path)
	old := umask(0o177)
	ln, err := net.Listen("unix", path)
	umask(old)
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, path: path, host: host, app: app, started: time.Now(), conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Path is the socket's path.
func (s *Server) Path() string { return s.path }

// Close stops listening, removes the socket and ends the connections, so
// their clients know.
func (s *Server) Close() error {
	err := s.ln.Close()
	os.Remove(s.path)
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	uid := os.Getuid()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		// The socket is its user's: anyone else's process, root's included,
		// is refused.
		if p := peerUID(conn); p >= 0 && p != uid {
			log.Printf("ctl: refused a connection of uid %d", p)
			conn.Close()
			continue
		}
		go s.serve(conn)
	}
}

// conn is a client: its answers and events go out through one writer.
type conn struct {
	c    net.Conn
	out  chan proto.Message
	done chan struct{}
	once sync.Once
}

func (c *conn) send(m proto.Message) {
	select {
	case c.out <- m:
	case <-c.done:
	}
}

// event queues an event, dropping it when the client is far behind: the
// next state replaces it.
func (c *conn) event(m proto.Message) {
	select {
	case c.out <- m:
	default:
	}
}

func (c *conn) close() { c.once.Do(func() { close(c.done); c.c.Close() }) }

func (s *Server) serve(nc net.Conn) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		nc.Close()
		return
	}
	s.conns[nc] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, nc)
		s.mu.Unlock()
	}()
	c := &conn{c: nc, out: make(chan proto.Message, 64), done: make(chan struct{})}
	defer c.close()
	r := bufio.NewReaderSize(nc, 64<<10)
	nc.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := proto.ReadLine(r, 64<<10)
	if err != nil {
		return
	}
	nc.SetReadDeadline(time.Time{})
	var hello proto.Hello
	if json.Unmarshal(line, &hello) != nil {
		return
	}
	enc := json.NewEncoder(nc)
	welcome := proto.Welcome{Version: proto.Version, PID: os.Getpid(), Started: s.started.UnixMilli(), App: s.app}
	if hello.Version != proto.Version {
		welcome.Error = "omaterm speaks protocol " + itoa(proto.Version) + ", this command " + itoa(hello.Version) + ": update it"
		enc.Encode(welcome)
		return
	}
	if hello.Kind == "attach" {
		if at, ok := s.host.(Attacher); ok {
			at.Attach(nc, r, hello, welcome)
			return
		}
		welcome.Error = "this server shows no panes"
		enc.Encode(welcome)
		return
	}
	if enc.Encode(welcome) != nil {
		return
	}
	go func() {
		for {
			select {
			case m := <-c.out:
				nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if enc.Encode(m) != nil {
					c.close()
					return
				}
			case <-c.done:
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var unsub func()
	defer func() {
		if unsub != nil {
			unsub()
		}
	}()
	for {
		line, err := proto.ReadLine(r, proto.MaxLine)
		if err != nil {
			return
		}
		var req proto.Request
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		if req.Method == "subscribe" {
			if unsub == nil {
				unsub = s.host.Subscribe(c.event)
			}
			c.send(proto.Message{ID: req.ID, Result: json.RawMessage("true")})
			continue
		}
		go func(req proto.Request) {
			res, err := s.host.Handle(ctx, req.Method, req.Params)
			m := proto.Message{ID: req.ID}
			if err != nil {
				m.Error, m.Code = err.Error(), proto.CodeOf(err)
			} else if b, err := json.Marshal(res); err == nil {
				m.Result = b
			}
			c.send(m)
		}(req)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

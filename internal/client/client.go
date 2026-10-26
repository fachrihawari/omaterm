// Partly adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/client/client.go.

// Package client calls omaterm's control socket: the omaterm command's and
// agents' hooks' way to the app.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"omaterm/internal/paths"
	"omaterm/internal/proto"
)

// ErrUnreachable is Connect's error when no omaterm serves the socket.
var ErrUnreachable = errors.New("omaterm is not running")

// Client is a connection to the control socket.
type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	mu      sync.Mutex
	next    int64
	calls   map[int64]chan proto.Message
	events  chan proto.Message
	closed  chan struct{}
	Welcome proto.Welcome
}

// Connect connects to the socket ($OMATERM_SOCKET, else omaterm's own).
func Connect() (*Client, error) {
	return Dial(paths.SocketPath())
}

// RefusedError is a server's refusal of the hello: another protocol's,
// as a session daemon of an older omaterm. Welcome tells its process.
type RefusedError struct{ Welcome proto.Welcome }

func (e *RefusedError) Error() string { return e.Welcome.Error }

// helloVersion is the protocol the client speaks (tests change it).
var helloVersion = proto.Version

// Dial connects to the socket at path.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, ErrUnreachable
	}
	c := &Client{conn: conn, enc: json.NewEncoder(conn), calls: map[int64]chan proto.Message{},
		events: make(chan proto.Message, 64), closed: make(chan struct{})}
	if err := c.enc.Encode(proto.Hello{Kind: "control", Version: helloVersion, Client: "omaterm"}); err != nil {
		conn.Close()
		return nil, ErrUnreachable
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := proto.ReadLine(r, 64<<10)
	if err != nil {
		conn.Close()
		return nil, ErrUnreachable
	}
	conn.SetReadDeadline(time.Time{})
	if err := json.Unmarshal(line, &c.Welcome); err != nil {
		conn.Close()
		return nil, ErrUnreachable
	}
	if c.Welcome.Error != "" {
		conn.Close()
		return nil, &RefusedError{Welcome: c.Welcome}
	}
	go c.read(r)
	return c, nil
}

func (c *Client) read(r *bufio.Reader) {
	defer close(c.events) // a subscriber's range ends with the connection
	defer close(c.closed)
	for {
		line, err := proto.ReadLine(r, proto.MaxLine)
		if err != nil {
			return
		}
		var m proto.Message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Event != "" {
			// Events are dropped when the subscriber is far behind (the
			// next "panes" replaces the last), but not a call passed on to
			// answer: the caller waits for it.
			if m.Event == "call" {
				select {
				case c.events <- m:
				case <-time.After(5 * time.Second):
				}
				continue
			}
			select {
			case c.events <- m:
			default:
			}
			continue
		}
		c.mu.Lock()
		ch := c.calls[m.ID]
		delete(c.calls, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// Call calls a method, decoding its result into out (when not nil).
func (c *Client) Call(method string, params, out any) error {
	return c.CallContext(context.Background(), method, params, out)
}

// CallContext is Call that gives up when ctx ends.
func (c *Client) CallContext(ctx context.Context, method string, params, out any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	ch := make(chan proto.Message, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.calls[id] = ch
	err := c.enc.Encode(proto.Request{ID: id, Method: method, Params: raw})
	c.mu.Unlock()
	if err != nil {
		return ErrUnreachable
	}
	select {
	case m := <-ch:
		if m.Error != "" {
			return &proto.Error{Code: m.Code, Msg: m.Error}
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-c.closed:
		return ErrUnreachable
	case <-ctx.Done():
		return proto.Errorf(proto.CodeTimeout, "timed out")
	}
}

// Events are the events of a subscribed client, until the connection
// ends.
func (c *Client) Events() <-chan proto.Message { return c.events }

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

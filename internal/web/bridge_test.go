//go:build linux || darwin

package web_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"

	"omaterm/internal/web"
)

// The page asks the terminal's socket whether it still carries, and the
// bridge answers between the frames.
func TestWebPong(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	ws := f.dial(t, c, "/api/panes/p1/attach")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if typ, _, err := ws.Read(ctx); err != nil || typ != websocket.MessageText {
		t.Fatalf("welcome: %v %v", typ, err)
	}
	ws.Write(ctx, websocket.MessageText, []byte("hello"))
	ws.Write(ctx, websocket.MessageText, []byte("ping"))
	for {
		typ, msg, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("no pong: %v", err)
		}
		if typ == websocket.MessageText {
			if string(msg) != "pong" {
				t.Fatalf("answered %q", msg)
			}
			return
		}
	}
}

// A socket whose page stopped answering pings, as a phone locked or off
// the network, is ended; one that answers stays.
func TestWebPingsEndDeadSockets(t *testing.T) {
	t.Cleanup(web.SetPingEvery(100 * time.Millisecond))
	f := newFixture(t)
	c := f.paired(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, path := range []string{"/api/events", "/api/panes/p1/attach"} {
		live, dead := f.dial(t, c, path), f.dial(t, c, path)
		ended := make(chan error, 1)
		go func() { // reading answers the pings
			for {
				if _, _, err := live.Read(ctx); err != nil {
					ended <- err
					return
				}
			}
		}()
		time.Sleep(time.Second)
		select {
		case err := <-ended:
			t.Fatalf("%s: a page answering pings was ended: %v", path, err)
		default:
		}
		readUntilClosed(t, dead, 5*time.Second)
	}
}

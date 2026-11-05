//go:build linux || darwin

package web_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/coder/websocket"

	"omaterm/internal/proto"
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

// A page out of sight gives the pane's size back through the bridge: the
// window's size comes back, and the page is told so.
func TestWebReleaseGivesSizeBack(t *testing.T) {
	f := newFixture(t)
	ours, theirs := net.Pipe()
	go func() {
		defer ours.Close()
		f.d.Attach(ours, bufio.NewReader(ours), proto.Hello{Kind: "attach", Version: proto.Version, Pane: "p1"}, proto.Welcome{Version: proto.Version})
	}()
	t.Cleanup(func() { theirs.Close() })
	br := bufio.NewReader(theirs)
	if _, err := br.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, br)
	theirs.Write(proto.AppendFrame(nil, proto.FrameResize, proto.Uint16s(100, 30, 9, 18)))

	c := f.paired(t)
	ws := f.dial(t, c, "/api/panes/p1/attach")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resized := func(cols, rows int) {
		t.Helper()
		for {
			typ, msg, err := ws.Read(ctx)
			if err != nil {
				t.Fatalf("never resized to %dx%d: %v", cols, rows, err)
			}
			if typ == websocket.MessageBinary && msg[0] == proto.FrameResized {
				if v, ok := proto.ParseUint16s(msg[5:], 2); ok && v[0] == cols && v[1] == rows {
					return
				}
			}
		}
	}
	ws.Write(ctx, websocket.MessageBinary, proto.AppendFrame(nil, proto.FrameResize, proto.Uint16s(40, 20, 9, 18)))
	resized(40, 20)
	ws.Write(ctx, websocket.MessageBinary, proto.AppendFrame(nil, proto.FrameRelease, nil))
	resized(100, 30)
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

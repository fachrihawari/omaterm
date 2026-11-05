package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// closeStopped is the close code of a page whose pane is a stopped process.
// Its reason is the message to show.
const closeStopped = 4410

// attach bridges a WebSocket to a pane's attach connection. The page
// first gets the welcome, as text: the pane's size. Then every binary
// message is one frame, as the attach socket has them. The page may type,
// send keys and pastes, and resize: a theme frame would recolor every
// pane. A text "ping" from the page is answered "pong".
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // secure checked the origin
	if err != nil {
		return
	}
	defer c.CloseNow()
	defer s.track(c, r)()
	c.SetReadLimit(1 << 20) // a pasted block
	// A stopped process has no pane to wake: the page is told which it is.
	if name := s.stoppedProcess(r.Context(), r.PathValue("id")); name != "" {
		c.Close(closeStopped, "process "+name+" isn't running: start it in omaterm")
		return
	}
	// A pane the window has not shown since it opened is not running yet.
	params, _ := json.Marshal(map[string]string{"id": r.PathValue("id")})
	if _, err := s.d.Handle(r.Context(), "pane.wake", params); err != nil {
		c.Close(4404, "no such pane")
		return
	}
	conn := s.d.AttachPipe(r.PathValue("id"))
	defer conn.Close()
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		c.Close(websocket.StatusInternalError, "the daemon did not answer")
		return
	}
	var welcome proto.Welcome
	if json.Unmarshal(line, &welcome) != nil || welcome.Error != "" {
		c.Close(4404, "no such pane")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go keepAlive(ctx, c)
	hello, _ := json.Marshal(map[string]int{"cols": welcome.Cols, "rows": welcome.Rows})
	if c.Write(ctx, websocket.MessageText, hello) != nil {
		return
	}

	// The pane's frames, to the page.
	go func() {
		defer cancel()
		for {
			kind, payload, err := proto.ReadFrame(br)
			if err != nil {
				c.Close(websocket.StatusNormalClosure, "detached")
				return
			}
			wctx, done := context.WithTimeout(ctx, 10*time.Second)
			err = c.Write(wctx, websocket.MessageBinary, proto.AppendFrame(nil, kind, payload))
			done()
			if err != nil {
				return
			}
			if kind == proto.FrameClosed {
				c.Close(websocket.StatusNormalClosure, "the pane closed")
				return
			}
		}
	}()

	// What the page types, and the size it asks for.
	for {
		typ, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			// The page, back from the background, asks whether the socket
			// still carries: a dead one may still say it is open there.
			if string(msg) == "ping" {
				wctx, done := context.WithTimeout(ctx, 10*time.Second)
				err := c.Write(wctx, websocket.MessageText, []byte("pong"))
				done()
				if err != nil {
					return
				}
			}
			continue
		}
		kind, payload, err := proto.ReadFrame(bytes.NewReader(msg))
		if err != nil || (kind != proto.FrameInput && kind != proto.FrameKey && kind != proto.FramePaste && kind != proto.FrameResize && kind != proto.FrameRelease) {
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(proto.AppendFrame(nil, kind, payload)); err != nil {
			return
		}
	}
}

// pingEvery is how often an open WebSocket is pinged, and how long its
// page has to answer: one gone without a word, as a phone locked or off
// the network, is ended within two, and its pane gets its size back; and
// a tunnel that cuts quiet connections sees something go by.
var pingEvery = 25 * time.Second

// keepAlive pings c until ctx ends, and drops it when a ping goes
// unanswered. c's reader takes the pongs.
func keepAlive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, pingEvery)
		err := c.Ping(pctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				c.CloseNow()
			}
			return
		}
	}
}

// stoppedProcess is the name of the process a pane belongs to when that
// process isn't running, whether stopped, exited or failed, and "" for any
// other pane: the state keeps the pane of the last run, as exited.
func (s *Server) stoppedProcess(ctx context.Context, pane string) string {
	v, err := s.d.Handle(ctx, "state", nil)
	if err != nil {
		return ""
	}
	// The state comes as the daemon has it, or as the app forwards it.
	raw, _ := json.Marshal(v)
	var st proto.State
	if json.Unmarshal(raw, &st) != nil {
		return ""
	}
	for _, sess := range st.Sessions {
		if sess.Process == "" {
			continue
		}
		for _, p := range sess.Panes {
			if p.ID == pane && p.Status == session.Exited {
				return sess.Process
			}
		}
	}
	return ""
}

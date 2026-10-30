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
// message is one frame, as the attach socket has them. The page may only
// type and resize: a theme frame would recolor every pane.
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
		c.Close(closeStopped, "process "+name+" is stopped: start it in omaterm")
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
			continue
		}
		kind, payload, err := proto.ReadFrame(bytes.NewReader(msg))
		if err != nil || (kind != proto.FrameInput && kind != proto.FrameResize) {
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(proto.AppendFrame(nil, kind, payload)); err != nil {
			return
		}
	}
}

// stoppedProcess is the name of the process a pane belongs to when that
// process is stopped, and "" for any other pane. A stopped process keeps its
// pane, which shows as exited.
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

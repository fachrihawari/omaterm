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
)

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

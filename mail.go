package main

import (
	"context"
	"encoding/json"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/mail"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// mail answers the mailbox's methods (package mail): the session daemon's
// mailbox, which holds its panes', else the app's own.
func (h *localHost) mail(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch panes := h.a.offPanes().(type) {
	case *daemon.Remote:
		out, err := panes.Forward(ctx, method, raw)
		if err != nil && ctx.Err() != nil && method == "mail.await" {
			// The command waiting went away, and the daemon goes on
			// waiting for the app: the answer goes to the asker instead.
			var p proto.MailAwaitParams
			json.Unmarshal(raw, &p)
			b, _ := json.Marshal(proto.IDParams{ID: p.ID})
			release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			panes.Forward(release, "mail.release", b)
		}
		return out, err
	case session.LocalHost:
		return h.mailFor(panes.Registry).Serve(ctx, method, raw)
	}
	return nil, proto.Errorf(proto.CodeUsage, "no mailbox")
}

// mailFor is the mailbox of reg's panes, the app's own, made as first
// asked for.
func (h *localHost) mailFor(reg *session.Registry) *mail.Mailbox {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mailbox == nil || h.mailReg != reg {
		if h.mailStop != nil {
			h.mailStop()
		}
		ctx, stop := context.WithCancel(context.Background())
		h.mailbox, h.mailReg, h.mailStop = mail.New(reg), reg, stop
		go h.mailbox.Run(ctx)
	}
	return h.mailbox
}

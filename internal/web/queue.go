package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"omaterm/internal/mail"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// Prompts the page lines up for an agent while it works: the mailbox types
// each into the agent's prompt once it is free, as it does omaterm's own
// notes, so the phone can go back in a pocket. Each is a note from omaterm
// itself: the page never says whom one is from, so it can't pass for
// another agent's message to a lead.

// queueFrom names the page's notes, to tell them from the others a pane
// has waiting, which the page neither shows nor takes back.
const queueFrom = "web access"

// maxQueued is how many of the page's prompts may wait for one pane.
const maxQueued = 5

// queued is a prompt waiting for its agent, as the page shows it.
type queued struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Created int64  `json:"created"`
	Why     string `json:"why,omitempty"` // why it waits: "it is working"
}

// call asks the daemon, and decodes its answer into out.
func (s *Server) call(ctx context.Context, method string, params, out any) error {
	raw, _ := json.Marshal(params)
	v, err := s.d.Handle(ctx, method, raw)
	if err != nil || out == nil {
		return err
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}

// agentPane finds pane id in the state with its session's name, as the
// daemon has it or the app forwards it.
func (s *Server) agentPane(ctx context.Context, id string) (proto.Pane, string, bool) {
	var st proto.State
	if s.call(ctx, "state", nil, &st) != nil {
		return proto.Pane{}, "", false
	}
	for _, sess := range st.Sessions {
		for _, p := range sess.Panes {
			if p.ID == id {
				return p, sess.Name, true
			}
		}
	}
	return proto.Pane{}, "", false
}

// queuedFor lists the page's prompts still waiting for pane.
func (s *Server) queuedFor(ctx context.Context, pane string) ([]queued, error) {
	var list []proto.Mail
	if err := s.call(ctx, "mail.list", proto.MailListParams{Pane: pane}, &list); err != nil {
		return nil, err
	}
	out := []queued{}
	for _, m := range list {
		if m.To == pane && m.From == "" && m.FromName == queueFrom && m.Note && m.State == proto.MailQueued {
			out = append(out, queued{ID: m.ID, Text: m.Text, Created: m.Created, Why: m.Why})
		}
	}
	return out, nil
}

// queue lists the prompts waiting for the pane's agent.
func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	list, err := s.queuedFor(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, list)
}

// enqueue lines up a prompt, {text}, for the pane's agent. A shell has
// no prompt to wait for: the page types into it instead.
func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Text string `json:"text"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p)
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig) || len(p.Text) > mail.MaxText:
		http.Error(w, "a prompt to send later is at most 64 KiB", http.StatusRequestEntityTooLarge)
		return
	case err != nil || strings.TrimSpace(p.Text) == "":
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	pane, name, ok := s.agentPane(r.Context(), id)
	switch {
	case !ok:
		http.Error(w, "no such pane", http.StatusNotFound)
		return
	case pane.Agent == "":
		http.Error(w, "no agent runs in this pane", http.StatusConflict)
		return
	case pane.Status == session.Exited:
		http.Error(w, "this session has ended", http.StatusConflict)
		return
	}
	// Counted and queued at once, as two pages may queue together.
	s.mu.Lock()
	defer s.mu.Unlock()
	waiting, err := s.queuedFor(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if len(waiting) >= maxQueued {
		http.Error(w, strconv.Itoa(maxQueued)+" prompts wait already: cancel one first", http.StatusTooManyRequests)
		return
	}
	var m proto.Mail
	if err := s.call(r.Context(), "mail.ask", proto.MailAskParams{FromName: queueFrom, To: id, ToName: name, Text: p.Text, Note: true}, &m); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, queued{ID: m.ID, Text: m.Text, Created: m.Created})
}

// unqueue takes back a prompt the page queued for the pane, while its
// agent hasn't been given it.
func (s *Server) unqueue(w http.ResponseWriter, r *http.Request) {
	waiting, err := s.queuedFor(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	msg := r.PathValue("msg")
	for _, q := range waiting {
		if q.ID != msg {
			continue
		}
		if err := s.call(r.Context(), "mail.cancel", proto.MailCancelParams{ID: msg}, nil); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, true)
		return
	}
	http.Error(w, "no such prompt waits: it may have been typed in", http.StatusNotFound)
}

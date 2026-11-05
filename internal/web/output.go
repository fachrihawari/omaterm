package web

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"omaterm/internal/proto"
)

// A pane's text, for the page to read, copy and search where the
// terminal's can't be selected: its scrollback and screen, as omaterm
// read --all has them, soft wraps joined. Reading is all: a stopped
// process is read, never started.

// maxText caps the text the page reads: its last megabyte.
var maxText = 1 << 20

// paneText is a pane's text as the page reads it.
type paneText struct {
	Text string `json:"text"`
	// More tells that earlier lines were left out, past maxText.
	More bool `json:"more,omitempty"`
	// Stopped names the process whose run ended: the text is the last
	// lines the daemon kept of it, "" when it kept none.
	Stopped string `json:"stopped,omitempty"`
}

// text is the pane's text. A pane the window has not shown since it
// opened starts first, as attach starts it; a stopped process's pane is
// gone, and the daemon answers with what it kept.
func (s *Server) text(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out := paneText{Stopped: s.stoppedProcess(ctx, id)}
	params, _ := json.Marshal(map[string]string{"id": id})
	if out.Stopped == "" {
		if _, err := s.d.Handle(ctx, "pane.wake", params); err != nil {
			http.Error(w, "no such pane", http.StatusNotFound)
			return
		}
	}
	params, _ = json.Marshal(proto.ReadParams{ID: id, Scrollback: true})
	v, err := s.d.Handle(ctx, "read", params)
	if err != nil && out.Stopped == "" {
		http.Error(w, "no such pane", http.StatusNotFound)
		return
	}
	if err == nil {
		raw, _ := json.Marshal(v)
		var res proto.ReadResult
		json.Unmarshal(raw, &res)
		out.Text = res.Text
	}
	if len(out.Text) > maxText {
		out.Text, out.More = out.Text[len(out.Text)-maxText:], true
		if i := strings.IndexByte(out.Text, '\n'); i >= 0 && i < len(out.Text)-1 {
			out.Text = out.Text[i+1:]
		}
	}
	// A megabyte of text is a tenth of it gzipped, over a phone's link.
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Add("Vary", "Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		json.NewEncoder(w).Encode(out)
		return
	}
	h.Set("Content-Encoding", "gzip")
	zw := gzip.NewWriter(w)
	json.NewEncoder(zw).Encode(out)
	zw.Close()
}

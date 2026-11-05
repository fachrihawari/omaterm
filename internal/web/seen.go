package web

import (
	"encoding/json"
	"net/http"

	"omaterm/internal/proto"
)

// seen tells the daemon the page shows a pane, in sight: its session's
// unread marks go, on the phone and the desktop alike. The session the
// desktop window shows stays its own. A POST, as it changes something,
// so secure checks that the page sent it.
func (s *Server) seen(w http.ResponseWriter, r *http.Request) {
	params, _ := json.Marshal(proto.IDParams{ID: r.PathValue("id")})
	if _, err := s.d.Handle(r.Context(), "seen", params); err != nil {
		if proto.CodeOf(err) == proto.CodeNotFound {
			http.Error(w, "no such pane", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, true)
}

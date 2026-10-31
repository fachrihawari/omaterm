package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

//go:embed assets
var assets embed.FS

// cookieName holds a paired device's id and secret.
const cookieName = "omaterm_device"

// Daemon is what the server needs of the session daemon.
type Daemon interface {
	// Handle answers a call, as the daemon's socket does.
	Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
	// AttachPipe attaches to a pane: the connection carries the welcome
	// line, then frames, as an attach socket does.
	AttachPipe(pane string) net.Conn
	// Watch calls fn as the panes change, until stopped. Unlike a socket
	// subscriber, a watcher is not an app.
	Watch(fn func()) (stop func())
}

// Server is the web access server.
type Server struct {
	d     Daemon
	store *Store
	srv   *http.Server
	ln    net.Listener

	// pairs limits pairing attempts: each client has its own count
	// (see pairClient), and all of them together a ceiling.
	mu       sync.Mutex
	pairs    map[string][]time.Time
	pairsAll []time.Time
	// conns are the open WebSockets and the devices they serve: closing
	// the server or signing a device out ends them, which http.Server
	// does not do for a connection it has handed over.
	conns map[*websocket.Conn]string
	done  chan struct{}
}

// sweepEvery is how often the open WebSockets' devices are checked, to
// end those signed out elsewhere, as by omaterm web revoke.
var sweepEvery = 5 * time.Second

// closeSignedOut is the close code that tells a page its device was
// signed out.
const closeSignedOut = 4401

// pairLimit is how many pairing attempts a minute are heard from one
// client, and pairLimitAll from all of them together.
const (
	pairLimit    = 10
	pairLimitAll = 60
)

// Listen serves on addr: the loopback, unless the config says the
// network.
func Listen(addr string, d Daemon, store *Store) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{d: d, store: store, ln: ln, conns: map[*websocket.Conn]string{}, done: make(chan struct{})}
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go s.sweep()
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("web: %v", err)
		}
	}()
	return s, nil
}

// Addr is where it listens.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops serving, ending the open pages' connections.
func (s *Server) Close() error {
	s.mu.Lock()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.mu.Unlock()
	s.end(func(string) bool { return true }, websocket.StatusGoingAway, "web access is off")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		return s.srv.Close()
	}
	return nil
}

// track counts a WebSocket as open for the request's device, until
// untracked.
func (s *Server) track(c *websocket.Conn, r *http.Request) (untrack func()) {
	d, _ := r.Context().Value(deviceKey{}).(Device)
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		c.Close(websocket.StatusGoingAway, "web access is off")
	default:
		s.conns[c] = d.ID
	}
	return func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}
}

// end closes the open WebSockets whose device matches.
func (s *Server) end(match func(device string) bool, code websocket.StatusCode, reason string) {
	s.mu.Lock()
	var ending []*websocket.Conn
	for c, device := range s.conns {
		if match(device) {
			ending = append(ending, c)
			delete(s.conns, c)
		}
	}
	s.mu.Unlock()
	for _, c := range ending {
		go c.Close(code, reason)
	}
}

// sweep ends, now and then, the WebSockets of devices no longer paired.
func (s *Server) sweep() {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		s.mu.Lock()
		open := len(s.conns)
		s.mu.Unlock()
		if open == 0 {
			continue
		}
		devices, err := s.store.Devices()
		if err != nil {
			continue
		}
		paired := map[string]bool{}
		for _, d := range devices {
			paired[d.ID] = true
		}
		s.end(func(device string) bool { return !paired[device] }, closeSignedOut, "signed out")
	}
}

// Handler is the server's routes; tests serve it themselves.
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(assets, "assets")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("POST /api/pair", s.pair)
	mux.HandleFunc("GET /api/state", s.auth(s.state))
	mux.HandleFunc("GET /api/theme", s.auth(s.theme))
	mux.HandleFunc("GET /api/events", s.auth(s.events))
	mux.HandleFunc("POST /api/sessions", s.auth(s.create))
	mux.HandleFunc("GET /api/panes/{id}/attach", s.auth(s.attach))
	mux.HandleFunc("POST /api/logout", s.auth(s.logout))
	return secure(mux)
}

// secure sets the headers every answer carries, and refuses a request
// that changes something from another site.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead || r.Header.Get("Upgrade") != "") && !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin tells whether a request comes from a page of this server:
// its Origin's host is the one asked for, or the one a tunnel in front
// forwarded. A browser cannot forge either for another site's page.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	for _, h := range []string{r.Host, r.Header.Get("X-Forwarded-Host")} {
		if h != "" && strings.EqualFold(u.Host, h) {
			return true
		}
	}
	return false
}

// auth lets a paired device through, its cookie renewed.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, "not paired", http.StatusUnauthorized)
			return
		}
		d, ok := s.store.Check(c.Value)
		if !ok {
			clearCookie(w, r)
			http.Error(w, "not paired", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Upgrade") == "" {
			setCookie(w, r, c.Value)
		}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, d)))
	}
}

type deviceKey struct{}

func setCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", MaxAge: int(deviceTTL / time.Second),
		HttpOnly: true, Secure: overTLS(r), SameSite: http.SameSiteStrictMode})
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: overTLS(r), SameSite: http.SameSiteStrictMode})
}

// overTLS tells a request that reached the tunnel over HTTPS; the
// loopback itself is plain HTTP.
func overTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// pairClient tells who a pairing attempt comes from, to count tries per
// client. A tunnel's requests all arrive from the loopback, and say who
// they carry in a header: Cloudflare's CF-Connecting-IP, else the last
// hop of X-Forwarded-For, which is the one the tunnel itself added. Those
// headers are believed only on a request from the loopback: from a network
// peer they are the peer's own claim.
func pairClient(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return host
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != nil {
		return ip.String()
	}
	if hops := strings.Split(r.Header.Get("X-Forwarded-For"), ","); len(hops) > 0 {
		if ip := net.ParseIP(strings.TrimSpace(hops[len(hops)-1])); ip != nil {
			return ip.String()
		}
	}
	return host
}

// recent keeps the times that are within the last minute.
func recent(times []time.Time, now time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	return kept
}

// allowPair tells whether a pairing attempt is heard: one client may try
// pairLimit times a minute, so that one cannot lock the others out, and all
// together pairLimitAll.
func (s *Server) allowPair(r *http.Request) bool {
	client := pairClient(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.pairs == nil {
		s.pairs = map[string][]time.Time{}
	}
	for c, times := range s.pairs {
		if times = recent(times, now); len(times) == 0 {
			delete(s.pairs, c)
		} else {
			s.pairs[c] = times
		}
	}
	s.pairsAll = recent(s.pairsAll, now)
	if len(s.pairs[client]) >= pairLimit || len(s.pairsAll) >= pairLimitAll {
		return false
	}
	s.pairs[client] = append(s.pairs[client], now)
	s.pairsAll = append(s.pairsAll, now)
	return true
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if !s.allowPair(r) {
		http.Error(w, "too many tries: wait a minute", http.StatusTooManyRequests)
		return
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cookie, d, err := s.store.Pair(p.Code, deviceName(r.UserAgent()))
	if errors.Is(err, ErrBadCode) {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("web: paired %s (%s)", d.Name, d.ID)
	setCookie(w, r, cookie)
	writeJSON(w, map[string]string{"device": d.Name})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	d := r.Context().Value(deviceKey{}).(Device)
	s.store.Revoke(d.ID)
	s.end(func(device string) bool { return device == d.ID }, closeSignedOut, "signed out")
	clearCookie(w, r)
	writeJSON(w, true)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Handle(r.Context(), "state", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, st)
}

// theme is the app's look, for the page to take: see Theme.
func (s *Server) theme(w http.ResponseWriter, r *http.Request) {
	t, err := s.d.Handle(r.Context(), "web.theme", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, t)
}

// event is what the events socket sends: the state, and the stamp of the
// theme, which the page fetches again when it is new.
type event struct {
	Type  string `json:"type"` // "state"
	State any    `json:"state"`
	Theme string `json:"theme,omitempty"`
}

// events sends the state as it changes, settled for a quarter second.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // secure checked the origin
	if err != nil {
		return
	}
	defer c.CloseNow()
	defer s.track(c, r)()
	ctx := c.CloseRead(r.Context())
	kick := make(chan struct{}, 1)
	stop := s.d.Watch(func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	})
	defer stop()
	send := func() error {
		st, err := s.d.Handle(ctx, "state", nil)
		if err != nil {
			return err
		}
		ev := event{Type: "state", State: st}
		if t, err := s.d.Handle(ctx, "web.theme", nil); err == nil {
			if th, ok := t.(Theme); ok {
				ev.Theme = th.Stamp
			}
		}
		b, _ := json.Marshal(ev)
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, b)
	}
	if send() != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			if send() != nil {
				return
			}
		}
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&p); err != nil || p.Project == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	params, _ := json.Marshal(map[string]string{"project": p.Project})
	res, err := s.d.Handle(r.Context(), "create", params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, res)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// deviceName names a browser by its User-Agent, roughly: "Chrome on
// Android".
func deviceName(ua string) string {
	browser := "Browser"
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"Firefox/", "Firefox"}, {"CriOS", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	for _, o := range []struct{ token, name string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			return browser + " on " + o.name
		}
	}
	return browser
}

// DefaultPort is where web access listens unless the settings say.
const DefaultPort = 7370

// Status tells whether web access serves, and where.
type Status struct {
	On    bool   `json:"on"`
	Addr  string `json:"addr,omitempty"`
	Error string `json:"error,omitempty"`
	// TunnelURL is where the running tunnel reaches it, when one does.
	TunnelURL string `json:"tunnelURL,omitempty"`
	// TunnelError is why the wanted tunnel isn't running.
	TunnelError string `json:"tunnelError,omitempty"`
}

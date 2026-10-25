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

	// pairs limits pairing attempts, for everyone at once: behind a
	// tunnel every request comes from the loopback.
	mu    sync.Mutex
	pairs []time.Time
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

// pairLimit is how many pairing attempts a minute are heard.
const pairLimit = 10

// Listen serves on addr, which must be on the loopback.
func Listen(addr string, d Daemon, store *Store) (*Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("web: " + addr + " is not on the loopback; a tunnel carries it further")
	}
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
		if (r.Method != http.MethodGet || r.Header.Get("Upgrade") != "") && !sameOrigin(r) {
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

func (s *Server) allowPair() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	kept := s.pairs[:0]
	for _, t := range s.pairs {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	s.pairs = kept
	if len(s.pairs) >= pairLimit {
		return false
	}
	s.pairs = append(s.pairs, now)
	return true
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if !s.allowPair() {
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
		b, _ := json.Marshal(st)
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
}

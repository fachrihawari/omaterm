package devurl

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
)

// Server is the proxy: it answers https for the hosts of its router.
type Server struct {
	ca     *CA
	router *Router
	ln     net.Listener
	srv    *http.Server
	done   chan struct{}
	once   sync.Once
}

// Listen starts the proxy on addr. The authority kept in caDir is loaded,
// or made, once the address is bound, so that a machine that cannot bind it
// gets no authority it has no use for.
func Listen(addr, caDir string, router *Router) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ca, err := LoadOrCreate(caDir)
	if err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{ca: ca, router: router, ln: ln, done: make(chan struct{})}
	s.srv = &http.Server{
		Handler:   s.handler(),
		TLSConfig: &tls.Config{GetCertificate: ca.GetCertificate, MinVersion: tls.VersionTLS12},
	}
	go func() {
		defer close(s.done)
		// ServeTLS adds h2 and http/1.1 to the protocols the client may pick.
		if err := s.srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return
		}
	}()
	return s, nil
}

// Addr is the address it listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Port is the port it listens on.
func (s *Server) Port() int {
	if a, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// CA is the authority its certificates are signed by.
func (s *Server) CA() *CA { return s.ca }

// Close stops it, and ends the connections it has.
func (s *Server) Close() {
	s.once.Do(func() {
		s.srv.Close()
		<-s.done
	})
}

func (s *Server) handler() http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			rt, _ := s.router.Lookup(r.In.Host)
			r.SetURL(&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(rt.Port))})
			// The dev server sees the host the browser asked for: it may
			// check it, and builds its own links from it.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
			r.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rt, _ := s.router.Lookup(r.Host)
			rt.Running = false
			rt.Status = "not answering"
			writePage(w, http.StatusBadGateway, notRunningPage, pageData{Route: rt})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := s.router.Lookup(r.Host)
		switch {
		case !ok:
			writePage(w, http.StatusNotFound, unknownHostPage, pageData{Host: hostOnly(r.Host), Routes: s.router.Routes()})
		case !rt.Running || rt.Port <= 0:
			writePage(w, http.StatusBadGateway, notRunningPage, pageData{Route: rt})
		default:
			proxy.ServeHTTP(w, r)
		}
	})
}

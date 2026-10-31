package devurl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// proxyFixture is a proxy on a free port, a backend behind it and a client
// that trusts the proxy's CA and reaches it whatever host it asks for.
type proxyFixture struct {
	srv     *Server
	router  *Router
	backend *httptest.Server
	client  *http.Client
	base    string
}

func newProxyFixture(t *testing.T, backend http.Handler) *proxyFixture {
	t.Helper()
	f := &proxyFixture{router: NewRouter(), backend: httptest.NewServer(backend)}
	t.Cleanup(f.backend.Close)
	var err error
	f.srv, err = Listen("127.0.0.1:0", t.TempDir(), f.router)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(f.srv.CA().Certificate())
	proxyAddr := f.srv.Addr()
	f.client = &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: pool},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, proxyAddr)
		},
	}}
	t.Cleanup(f.client.CloseIdleConnections)
	f.base = ":" + strconv.Itoa(f.srv.Port())
	return f
}

func (f *proxyFixture) backendPort() int {
	_, p, _ := net.SplitHostPort(f.backend.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func (f *proxyFixture) get(t *testing.T, host string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "https://"+host+f.base+"/hello?x=1", nil)
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestProxyForwardsToTheBackend(t *testing.T) {
	var seen *http.Request
	f := newProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		io.WriteString(w, "shop says hi at "+r.URL.RequestURI())
	}))
	f.router.Set([]Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: f.backendPort(), Running: true}})
	resp, body := f.get(t, "shop.localhost")
	if resp.StatusCode != 200 || body != "shop says hi at /hello?x=1" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if resp.ProtoMajor != 2 {
		t.Errorf("served over HTTP/%d, want 2", resp.ProtoMajor)
	}
	if seen.Header.Get("X-Forwarded-Proto") != "https" {
		t.Errorf("X-Forwarded-Proto %q", seen.Header.Get("X-Forwarded-Proto"))
	}
	if got := seen.Header.Get("X-Forwarded-Host"); got != "shop.localhost"+f.base {
		t.Errorf("X-Forwarded-Host %q", got)
	}
	if seen.Header.Get("X-Forwarded-For") != "127.0.0.1" {
		t.Errorf("X-Forwarded-For %q", seen.Header.Get("X-Forwarded-For"))
	}
	if seen.Host != "shop.localhost"+f.base {
		t.Errorf("the backend saw the host %q", seen.Host)
	}
}

// TestProxyReachesABackendOnIPv6Loopback: on macOS "localhost" is ::1
// first, and a dev server that listens on localhost, as Vite does by
// default, listens there only.
func TestProxyReachesABackendOnIPv6Loopback(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	backend := &httptest.Server{Listener: ln, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "vite on ::1")
	})}}
	backend.Start()
	t.Cleanup(backend.Close)
	f := newProxyFixture(t, http.NotFoundHandler())
	f.router.Set([]Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: ln.Addr().(*net.TCPAddr).Port, Running: true}})
	if resp, body := f.get(t, "shop.localhost"); resp.StatusCode != 200 || body != "vite on ::1" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

func TestProxyCarriesWebSockets(t *testing.T) {
	f := newProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, msg, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if c.Write(r.Context(), typ, append([]byte("echo: "), msg...)) != nil {
				return
			}
		}
	}))
	f.router.Set([]Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: f.backendPort(), Running: true}})
	// A websocket goes over HTTP/1.1, whatever the page was served over.
	h1 := f.client.Transport.(*http.Transport).Clone()
	h1.ForceAttemptHTTP2 = false
	h1.TLSClientConfig = h1.TLSClientConfig.Clone()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "wss://shop.localhost"+f.base+"/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: h1}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("reload")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := c.Read(ctx)
	if err != nil || string(msg) != "echo: reload" {
		t.Fatalf("%q %v", msg, err)
	}
}

func TestProxyShowsThePageOfAProcessThatIsNotRunning(t *testing.T) {
	f := newProxyFixture(t, http.NotFoundHandler())
	f.router.Set([]Route{
		{Host: "shop.localhost", Project: "x", Process: "web", Port: 0, Running: false, Status: "stopped"},
		{Host: "late.localhost", Project: "x", Process: "late", Port: f.backendPort(), Running: false, Status: "starting"},
	})
	for host, status := range map[string]string{"shop.localhost": "Status: stopped", "late.localhost": "Status: starting"} {
		resp, body := f.get(t, host)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "isn't running") || !strings.Contains(body, status) {
			t.Fatalf("%s: %d %q", host, resp.StatusCode, body)
		}
	}
}

func TestProxyShowsThePageOfAProcessThatDoesNotAnswer(t *testing.T) {
	f := newProxyFixture(t, http.NotFoundHandler())
	// A port nothing listens on: the backend is closed first.
	port := f.backendPort()
	f.backend.Close()
	f.router.Set([]Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: port, Running: true, Status: "running"}})
	resp, body := f.get(t, "shop.localhost")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "web isn't running") || !strings.Contains(body, "not answering") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

func TestProxyListsTheRoutesOfAnUnknownHost(t *testing.T) {
	f := newProxyFixture(t, http.NotFoundHandler())
	f.router.Set([]Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: 1, Running: true}})
	resp, body := f.get(t, "nope.localhost")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "nope.localhost") || !strings.Contains(body, "shop.localhost") {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	f.router.Set(nil)
	_, body = f.get(t, "nope.localhost")
	if !strings.Contains(body, "No process has a URL") {
		t.Fatalf("%q", body)
	}
}

func TestPagesEscapeWhatTheyShow(t *testing.T) {
	f := newProxyFixture(t, http.NotFoundHandler())
	f.router.Set([]Route{{Host: "shop.localhost", Project: "<script>x</script>", Process: "web"}})
	_, body := f.get(t, "shop.localhost")
	if strings.Contains(body, "<script>x") {
		t.Fatalf("project name not escaped: %q", body)
	}
}

func TestListenFailureLeavesNoAuthority(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dir := t.TempDir() + "/devurl"
	if _, err := Listen(ln.Addr().String(), dir, NewRouter()); err == nil {
		t.Fatal("bound a taken address")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an authority was made though nothing was bound: %v", err)
	}
}

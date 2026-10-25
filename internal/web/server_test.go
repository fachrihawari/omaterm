//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"omaterm/internal/daemon"
	"omaterm/internal/proto"
	"omaterm/internal/web"
)

type fixture struct {
	d     *daemon.Daemon
	base  string // http://127.0.0.1:port
	store *web.Store
}

// newFixture runs a daemon with web access on, and one pane in it, "p1",
// which echoes what it is sent.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "omw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	store := web.OpenStore(web.StorePath(dir))
	store.SetConfig(func(c *web.Config) { c.Enabled, c.Port = true, port })
	d, err := daemon.Start(daemon.Options{Socket: filepath.Join(dir, "d.sock"), StateDir: dir, StateFile: filepath.Join(dir, "state.json"), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	params, _ := json.Marshal(daemon.StartParams{ID: "p1", Command: []string{"sh", "-c", "echo ready; exec cat"}, Exec: true, Cols: 80, Rows: 24})
	if _, err := d.Handle(context.Background(), "pane.start", params); err != nil {
		t.Fatal(err)
	}
	return &fixture{d: d, base: fmt.Sprintf("http://127.0.0.1:%d", port), store: store}
}

// client is a browser: a cookie jar, and the Origin its pages send.
func (f *fixture) client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func (f *fixture) post(t *testing.T, c *http.Client, path, body, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", f.base+path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *fixture) paired(t *testing.T) *http.Client {
	t.Helper()
	c := f.client(t)
	code, _, err := f.store.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if res := f.post(t, c, "/api/pair", `{"code":"`+code+`"}`, f.base); res.StatusCode != 200 {
		t.Fatalf("pair: %s", res.Status)
	}
	return c
}

func (f *fixture) dial(t *testing.T, c *http.Client, path string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(f.base)
	h := http.Header{"Origin": {f.base}}
	for _, k := range c.Jar.Cookies(u) {
		h.Add("Cookie", k.Name+"="+k.Value)
	}
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(f.base, "http")+path, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// Nothing but the page and pairing answers an unpaired browser, and
// nothing that changes anything answers another site.
func TestWebNeedsPairing(t *testing.T) {
	f := newFixture(t)
	c := f.client(t)
	res, err := c.Get(f.base + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired state: %s", res.Status)
	}
	if res, _ := c.Get(f.base + "/"); res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("page: %s, CSP %q", res.Status, res.Header.Get("Content-Security-Policy"))
	}
	code, _, _ := f.store.NewCode()
	for _, origin := range []string{"", "https://evil.example"} {
		if res := f.post(t, c, "/api/pair", `{"code":"`+code+`"}`, origin); res.StatusCode != http.StatusForbidden {
			t.Fatalf("pair from origin %q: %s", origin, res.Status)
		}
	}
	if res := f.post(t, c, "/api/pair", `{"code":"0000-0000"}`, f.base); res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong code: %s", res.Status)
	}
	// A tunnel that forwards its own host is the page's origin too.
	req, _ := http.NewRequest("POST", f.base+"/api/pair", strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Origin", "https://box.tailnet.ts.net")
	req.Header.Set("X-Forwarded-Host", "box.tailnet.ts.net")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err = c.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("pair through a tunnel: %v %v", res.Status, err)
	}
	if ck := res.Cookies(); len(ck) != 1 || !ck[0].HttpOnly || !ck[0].Secure || ck[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie through a tunnel: %+v", ck)
	}
}

func TestWebStateAndEvents(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	res, err := c.Get(f.base + "/api/state")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("state: %v %v", res.Status, err)
	}
	var st proto.State
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	ws := f.dial(t, c, "/api/events")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, msg, err := ws.Read(ctx); err != nil || !json.Valid(msg) {
		t.Fatalf("first event: %v", err)
	}
	// A page watching is no app: the daemon still notifies by itself.
	got, _ := f.d.Handle(ctx, "clients", nil)
	if got.(proto.Clients).Apps != 0 {
		t.Fatalf("a web page counts as an app: %+v", got)
	}
	// Logging out signs the device out.
	f.post(t, c, "/api/logout", "", f.base)
	if res, _ := c.Get(f.base + "/api/state"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: %s", res.Status)
	}
}

// A pane shows in the page as frames, and what the page types reaches
// it; it cannot recolor the panes.
func TestWebAttach(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	ws := f.dial(t, c, "/api/panes/p1/attach")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, msg, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		t.Fatalf("welcome: %v %v", typ, err)
	}
	var size struct{ Cols, Rows int }
	json.Unmarshal(msg, &size)
	if size.Cols != 80 || size.Rows != 24 {
		t.Fatalf("welcome size %+v", size)
	}
	typ, msg, err = ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || msg[0] != proto.FrameOutput {
		t.Fatalf("first frame: %v %q %v", typ, msg[:min(len(msg), 1)], err)
	}
	ws.Write(ctx, websocket.MessageBinary, proto.AppendFrame(nil, proto.FrameTheme, make([]byte, 19*4)))
	ws.Write(ctx, websocket.MessageBinary, proto.AppendFrame(nil, proto.FrameInput, []byte("typed-from-web\n")))
	go func() { // keep reading, as a page does
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()
	params, _ := json.Marshal(proto.ReadParams{ID: "p1"})
	for {
		r, err := f.d.Handle(ctx, "read", params)
		if err == nil && strings.Count(r.(proto.ReadResult).Text, "typed-from-web") >= 2 { // echoed, and cat's copy
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("typing did not reach the pane: %v %v", r, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWebUnknownPane(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	ws := f.dial(t, c, "/api/panes/nope/attach")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := ws.Read(ctx)
	if websocket.CloseStatus(err) != 4404 {
		t.Fatalf("unknown pane: %v", err)
	}
	_ = io.EOF
}

// readUntilClosed reads a WebSocket as a page does, and returns how it
// was closed.
func readUntilClosed(t *testing.T, ws *websocket.Conn, within time.Duration) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatalf("still open after %v", within)
			}
			return websocket.CloseStatus(err)
		}
	}
}

// Signing a device out elsewhere, as omaterm web revoke does, ends the
// terminals it has open.
func TestWebRevokeEndsTerminal(t *testing.T) {
	t.Cleanup(web.SetSweepEvery(100 * time.Millisecond))
	f := newFixture(t)
	c := f.paired(t)
	term := f.dial(t, c, "/api/panes/p1/attach")
	events := f.dial(t, c, "/api/events")
	other := f.paired(t)
	kept := f.dial(t, other, "/api/events")
	devices, _ := f.store.Devices()
	if _, err := f.store.Revoke(devices[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*websocket.Conn{term, events} {
		if code := readUntilClosed(t, ws, 5*time.Second); code != 4401 {
			t.Fatalf("closed with %v, not signed out", code)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for { // the other device's page stays
		if _, _, err := kept.Read(ctx); err != nil {
			if ctx.Err() == nil {
				t.Fatalf("the other device was ended too: %v", err)
			}
			break
		}
	}
}

// Turning web access off ends the open terminals: the HTTP server alone
// would leave them be.
func TestWebOffEndsTerminal(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	term := f.dial(t, c, "/api/panes/p1/attach")
	f.store.SetConfig(func(c *web.Config) { c.Enabled = false })
	ctx := context.Background()
	if _, err := f.d.Handle(ctx, "web.reload", nil); err != nil {
		t.Fatal(err)
	}
	if code := readUntilClosed(t, term, 5*time.Second); code != websocket.StatusGoingAway {
		t.Fatalf("closed with %v", code)
	}
}

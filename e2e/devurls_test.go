//go:build e2e

package e2e

// Dev URLs end to end, with the built binary: a process with a url runs a
// server on the PORT it is given, and the daemon's proxy serves it over
// https at its name, under a certificate the daemon's own authority signed.
// The proxy listens on a high port: binding 443 takes the setup's privilege,
// which no test may ask for.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"omaterm/internal/procs"
)

const urlYML = `processes:
  web:
    command: exec python3 -m http.server "$PORT" --bind 127.0.0.1
    url: shop
`

func TestDevURLServesAProcessOverHTTPS(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 serves the page")
	}
	a := Start(t, Options{Daemon: true})
	a.Quit()

	project := filepath.Join(a.dir, "project")
	if err := os.WriteFile(filepath.Join(project, procs.FileName), []byte(urlYML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "index.html"), []byte("hello from shop"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.addProject("p1", "proj", project)
	a.trust(project, procs.Def{Name: "web", Command: `exec python3 -m http.server "$PORT" --bind 127.0.0.1`, URL: "shop"})

	// The proxy's port: the daemon the next call starts reads it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	proxy := "127.0.0.1:" + strconv.Itoa(port)
	a.env = append(a.env, "OMATERM_PROXY_ADDR="+proxy)
	a.reload()

	a.MustCLI("start", "web", "--wait")
	p := a.proc(t, "web")
	if p.Status != procs.Running || p.Port == 0 {
		t.Fatalf("after start: %+v", p)
	}
	if want := "https://shop.localhost:" + strconv.Itoa(port); p.Link != want {
		t.Fatalf("ps shows the link %q, want %q", p.Link, want)
	}

	// A client that trusts the authority in the daemon's state, and reaches
	// the proxy whatever name it asks for.
	caPEM, err := os.ReadFile(filepath.Join(a.dir, "state", "omaterm", "devurl", "ca.pem"))
	if err != nil {
		t.Fatalf("the daemon made no authority: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("the authority isn't PEM")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, proxy)
		},
	}}
	get := func(host string) (int, string) {
		t.Helper()
		resp, err := client.Get("https://" + host + ":" + strconv.Itoa(port) + "/")
		if err != nil {
			t.Fatalf("GET %s: %v", host, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("shop.localhost"); code != 200 || !strings.Contains(body, "hello from shop") {
		t.Fatalf("through the proxy: %d %q", code, body)
	}
	if code, body := get("nobody.localhost"); code != http.StatusNotFound || !strings.Contains(body, "shop.localhost") {
		t.Fatalf("an unknown host: %d %q", code, body)
	}

	// The CLI shows the same: the proxy running, and the route.
	var st struct {
		Running bool
		Addr    string
		Routes  []struct {
			Host    string
			Running bool
		}
	}
	out := a.MustCLI("proxy", "status", "--json")
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Running || st.Addr != proxy || len(st.Routes) != 1 || st.Routes[0].Host != "shop.localhost" || !st.Routes[0].Running {
		t.Fatalf("proxy status: %v\n%s", err, out)
	}

	// Stopped, the name answers with the page that says so, and the server
	// is gone: a process's program doesn't outlive its stop.
	a.MustCLI("stop", "web")
	if !programEnded(p.PID) {
		t.Fatalf("the server (pid %d) still runs after stop", p.PID)
	}
	if code, body := get("shop.localhost"); code != http.StatusBadGateway || !strings.Contains(body, "web isn't running") {
		t.Fatalf("after stop: %d %q", code, body)
	}

	// Nor does it outlive the daemon: started again, then the daemon stops.
	a.MustCLI("start", "web", "--wait")
	again := a.proc(t, "web")
	if again.PID == 0 || again.PID == p.PID {
		t.Fatalf("after the second start: %+v", again)
	}
	a.MustCLI("daemon", "stop")
	if !programEnded(again.PID) {
		t.Fatalf("the server (pid %d) outlived the daemon", again.PID)
	}
}

// programEnded waits a moment for a program to end: a zombie not yet reaped
// has ended too.
func programEnded(pid int) bool {
	return poll(3*time.Second, func() bool {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		return err != nil || strings.Contains(string(b), ") Z ")
	})
}

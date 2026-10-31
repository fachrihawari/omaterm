//go:build linux || darwin

package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

// proxyGet fetches https://host/ through the daemon's proxy, trusting its CA.
func proxyGet(t *testing.T, d *Daemon, addr, host string) (int, string) {
	t.Helper()
	pem, err := os.ReadFile(filepath.Join(d.opts.StateDir, "devurl", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	defer c.CloseIdleConnections()
	_, port, _ := net.SplitHostPort(addr)
	resp, err := c.Get("https://" + host + ":" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func procByName(list []proto.Process, name string) proto.Process {
	for _, p := range list {
		if p.Name == name {
			return p
		}
	}
	return proto.Process{}
}

func TestDaemonServesAProcessAtItsURL(t *testing.T) {
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeProcesses(t, projectDir, "processes:\n  web:\n    command: OMATERM_TEST_HTTP=1 exec "+exe+"\n    url: shop\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	st := call[ProxyStatus](t, c, "proxy.status", nil)
	if !st.Running || st.Error != "" || len(st.Routes) != 1 || st.Routes[0].Host != "shop.localhost" || st.Routes[0].Running {
		t.Fatalf("status before the start: %+v", st)
	}
	// Defined but not started, its link is already the address it will have.
	_, port, _ := net.SplitHostPort(st.Addr)
	link := "https://shop.localhost:" + port
	if got := procByName(call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"}), "web").Link; got != link {
		t.Fatalf("link %q, want %q", got, link)
	}
	if code, body := proxyGet(t, d, st.Addr, "shop.localhost"); code != http.StatusBadGateway || !strings.Contains(body, "web isn't running") {
		t.Fatalf("not running: %d %q", code, body)
	}

	call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: "web"})
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	var proc proto.Process
	waitFor(t, "the process running", func() bool {
		proc = procByName(call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"}), "web")
		return proc.Status == procs.Running
	})
	if proc.Port == 0 || proc.Link != link {
		t.Fatalf("process: port %d link %q", proc.Port, proc.Link)
	}
	st = call[ProxyStatus](t, c, "proxy.status", nil)
	rt := st.Routes[0]
	if rt.Port != proc.Port || !rt.Running || rt.Project != "proj" || rt.Process != "web" {
		t.Fatalf("route: %+v, process port %d", rt, proc.Port)
	}
	// What the dev server was told is the port the route has, and it sees
	// the host and protocol the browser used.
	code, body := proxyGet(t, d, st.Addr, "shop.localhost")
	want := "port=" + strconv.Itoa(proc.Port) + " host=shop.localhost:" + port + " proto=https"
	if code != 200 || body != want {
		t.Fatalf("through the proxy: %d %q, want %q", code, body, want)
	}

	call[proto.Process](t, c, "process.stop", proto.ProcessParams{Project: "p1", Name: "web"})
	if st = call[ProxyStatus](t, c, "proxy.status", nil); st.Routes[0].Running {
		t.Fatalf("a stopped process routes as running: %+v", st.Routes[0])
	}
}

func TestDaemonURLTakenByAnotherProcess(t *testing.T) {
	d, socket, projectDir := offlineDaemon(t, false, nil)
	writeProcesses(t, projectDir, "processes:\n  api:\n    command: sleep 30\n    url: shop\n  web:\n    command: sleep 30\n    url: shop\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
	api, web := procByName(list, "api"), procByName(list, "web")
	if !strings.HasPrefix(api.Link, "https://shop.localhost:") || api.Message != "" {
		t.Fatalf("the first: link %q message %q", api.Link, api.Message)
	}
	if web.Link != "" || web.Message != "shop.localhost is taken by api in project proj" {
		t.Fatalf("the second: link %q message %q", web.Link, web.Message)
	}
	st := d.proxyStatus()
	if len(st.Routes) != 1 || st.Routes[0].Process != "api" {
		t.Fatalf("routes %+v", st.Routes)
	}
}

func TestDaemonProxyThatCannotBind(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	dir, err := os.MkdirTemp("", "omd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	proj := filepath.Join(dir, "proj")
	os.Mkdir(proj, 0o700)
	writeProcesses(t, proj, "processes:\n  web:\n    command: sleep 30\n    url: shop\n")
	stateFile := filepath.Join(dir, "state.json")
	if err := model.Save(stateFile, model.State{Version: model.Version, ActiveProject: "p1",
		Projects: []model.Project{{ID: "p1", Name: "proj", Dir: proj}}}); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: stateFile, IdleExit: time.Minute, Version: "test", ProxyAddr: taken.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})

	st := call[ProxyStatus](t, c, "proxy.status", nil)
	if st.Running || st.Error == "" || st.Addr != taken.Addr().String() || len(st.Routes) != 1 {
		t.Fatalf("status: %+v", st)
	}
	if got := procByName(call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"}), "web"); got.Link != "" {
		t.Fatalf("a link without a proxy: %q", got.Link)
	}
	if _, err := os.Stat(filepath.Join(dir, "devurl")); !os.IsNotExist(err) {
		t.Fatalf("a CA was made without a proxy: %v", err)
	}

	// Once the address is free, a reload binds it and the links appear.
	taken.Close()
	st = call[ProxyStatus](t, c, "proxy.reload", nil)
	if !st.Running || st.Error != "" {
		t.Fatalf("after the reload: %+v", st)
	}
	if got := procByName(call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"}), "web"); got.Link == "" {
		t.Fatal("no link after the proxy runs")
	}
	if _, err := os.Stat(filepath.Join(dir, "devurl", "ca.pem")); err != nil {
		t.Fatalf("no CA after the proxy runs: %v", err)
	}
	// A reload while it runs moves nothing wrong: it rebinds.
	if st = call[ProxyStatus](t, c, "proxy.reload", nil); !st.Running {
		t.Fatalf("second reload: %+v", st)
	}
}

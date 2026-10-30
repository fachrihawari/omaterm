//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"omaterm/internal/daemon"
	"omaterm/internal/model"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/web"
)

// newProcessFixture is newFixture for a project that defines a process,
// web, and trusts it. The process has not started.
func newProcessFixture(t *testing.T) *fixture {
	t.Helper()
	// A short folder, for the socket's path; the trust goes in its config.
	dir, err := os.MkdirTemp("", "omw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	project := filepath.Join(dir, "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, procs.FileName), []byte("processes:\n  web:\n    command: sleep 600\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defs, err := procs.LoadFile(project)
	if err != nil || len(defs) != 1 {
		t.Fatalf("defs %v %v", defs, err)
	}
	if err := procs.OpenTrust(procs.TrustPath()).Add(project, defs[0].Variant()); err != nil {
		t.Fatal(err)
	}
	st := model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "app", Dir: project}}}
	if err := model.Save(filepath.Join(dir, "state.json"), st); err != nil {
		t.Fatal(err)
	}

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
	return &fixture{dir: dir, d: d, base: fmt.Sprintf("http://127.0.0.1:%d", port), store: store}
}

// processSession is the state's session of the process named, once it is in it.
func processSession(t *testing.T, c *http.Client, f *fixture, name string) proto.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		res, err := c.Get(f.base + "/api/state")
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("state: %v", err)
		}
		var st proto.State
		err = json.NewDecoder(res.Body).Decode(&st)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range st.Sessions {
			if s.Process == name {
				return s
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("no session of process %s in the state", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Stopping a process keeps its pane, as exited. The state tells the page
// which session is a process, and the pane of a stopped one cannot be
// attached: the page is told why.
func TestWebProcessSession(t *testing.T) {
	f := newProcessFixture(t)
	c := f.paired(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	params, _ := json.Marshal(proto.ProcessParams{Project: "p1", Name: "web"})
	if _, err := f.d.Handle(ctx, "process.start", params); err != nil {
		t.Fatal(err)
	}
	running := func() proto.Session {
		for {
			s := processSession(t, c, f, "web")
			if s.Panes[0].Status != "exited" {
				return s
			}
			if ctx.Err() != nil {
				t.Fatal("the process did not start")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	if running.Process != "web" || running.Name != "web" {
		t.Fatalf("session %+v", running)
	}

	// Running, its pane attaches.
	ws := f.dial(t, c, "/api/panes/"+running.Panes[0].ID+"/attach")
	if typ, _, err := ws.Read(ctx); err != nil || typ != websocket.MessageText {
		t.Fatalf("welcome of a running process: %v %v", typ, err)
	}

	if _, err := f.d.Handle(ctx, "process.stop", params); err != nil {
		t.Fatal(err)
	}
	for processSession(t, c, f, "web").Panes[0].Status != "exited" {
		if ctx.Err() != nil {
			t.Fatal("the process did not stop")
		}
		time.Sleep(50 * time.Millisecond)
	}

	stopped := processSession(t, c, f, "web")
	ws = f.dial(t, c, "/api/panes/"+stopped.Panes[0].ID+"/attach")
	_, _, err := ws.Read(ctx)
	var ce websocket.CloseError
	const want = "process web is stopped: start it in omaterm"
	if !errors.As(err, &ce) || ce.Code != 4410 || ce.Reason != want {
		t.Fatalf("stopped process: %v, want close 4410 %q", err, want)
	}
}

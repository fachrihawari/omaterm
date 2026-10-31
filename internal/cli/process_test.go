package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/tunnel"
)

// fakeHost answers the control socket with one handler per method.
type fakeHost map[string]func(params json.RawMessage) (any, error)

func (h fakeHost) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	fn, ok := h[method]
	if !ok {
		return nil, fmt.Errorf("no fake for %s", method)
	}
	return fn(params)
}

func (fakeHost) Subscribe(func(proto.Message)) func() { return func() {} }

// fakeSocket serves h on a socket of its own, and points omaterm at it.
func fakeSocket(t *testing.T, h fakeHost) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.sock")
	srv, err := ctl.Listen(path, h, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	t.Setenv("OMATERM_SOCKET", path)
	return path
}

// stdout runs fn and returns what it printed to standard output.
func stdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	fn()
	os.Stdout = old
	w.Close()
	return <-out
}

// projectFolder is a project folder, with the working directory in it.
func projectFolder(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// stateOf answers state with one project, in dir.
func stateOf(dir string) func(json.RawMessage) (any, error) {
	return func(json.RawMessage) (any, error) {
		return proto.State{Projects: []proto.Project{{ID: "p1", Name: "app", Dir: dir}}}, nil
	}
}

// listOf answers process.list with the processes given.
func listOf(list ...proto.Process) func(json.RawMessage) (any, error) {
	return func(json.RawMessage) (any, error) { return list, nil }
}

var fixture = []proto.Process{
	{Def: procs.Def{Name: "web", Command: "npm run dev", Port: 3000, URL: "shop"}, Status: procs.Running, PID: 4321,
		Link: "https://shop.localhost"},
	{Def: procs.Def{Name: "worker", Command: "go run ./cmd/worker"}, Status: procs.Untrusted,
		Message: "worker isn't trusted yet: review it in omaterm"},
	{Def: procs.Def{Name: "queue", Command: "redis-server"}, Status: procs.Failed, Restarts: 2,
		Message: "exited (code 1)"},
}

func TestPs(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.list": listOf(fixture...)})
	var code int
	out := stdout(t, func() { code = Run([]string{"ps"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || strings.Fields(lines[0])[0] != "NAME" {
		t.Fatalf("got %q", out)
	}
	want := []string{
		"web running 3000 4321 - https://shop.localhost -",
		"worker untrusted - - - - worker isn't trusted yet: review it in omaterm",
		"queue failed - - 2 - exited (code 1)",
	}
	for i, w := range want {
		if got := strings.Join(strings.Fields(lines[i+1]), " "); got != w {
			t.Errorf("row %d: got %q, want %q", i, got, w)
		}
	}
}

func TestPsJSON(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.list": listOf(fixture...)})
	var code int
	out := stdout(t, func() { code = Run([]string{"ps", "--json"}) })
	var got []proto.Process
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 3 || got[1].Name != "worker" || got[1].Status != procs.Untrusted {
		t.Fatalf("exit %d, got %q", code, out)
	}
}

func TestPsNoProcesses(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.list": listOf()})
	var code int
	out := stdout(t, func() { code = Run([]string{"ps"}) })
	if code != 0 || out != "" {
		t.Errorf("exit %d, stdout %q; want 0 and nothing on stdout", code, out)
	}
}

func TestStartUntrusted(t *testing.T) {
	dir := projectFolder(t)
	const msg = "web isn't trusted yet: review it in omaterm"
	fakeSocket(t, fakeHost{
		"state": stateOf(dir),
		"process.start": func(json.RawMessage) (any, error) {
			return nil, proto.Errorf("", msg)
		},
	})
	if code := Run([]string{"start", "web"}); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	path := os.Getenv("OMATERM_SOCKET")
	c, err := client.Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = processCmd(c, "process.start", []string{"web"})
	if err == nil || err.Error() != msg {
		t.Errorf("got %v, want %q", err, msg)
	}
}

func TestStartWait(t *testing.T) {
	dir := projectFolder(t)
	var polls atomic.Int32
	fakeSocket(t, fakeHost{
		"state": stateOf(dir),
		"process.start": func(json.RawMessage) (any, error) {
			return proto.Process{Def: procs.Def{Name: "web"}, Status: procs.Starting}, nil
		},
		"process.list": func(json.RawMessage) (any, error) {
			if polls.Add(1) == 1 {
				return []proto.Process{{Def: procs.Def{Name: "web"}, Status: procs.Starting}}, nil
			}
			return []proto.Process{{Def: procs.Def{Name: "web"}, Status: procs.Running, PID: 9}}, nil
		},
	})
	var code int
	stdout(t, func() { code = Run([]string{"start", "web", "--wait"}) })
	if code != 0 || polls.Load() < 2 {
		t.Errorf("exit %d after %d polls, want 0 after at least 2", code, polls.Load())
	}
}

func TestStartWaitFailed(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{
		"state": stateOf(dir),
		"process.start": func(json.RawMessage) (any, error) {
			return proto.Process{Def: procs.Def{Name: "queue"}, Status: procs.Starting}, nil
		},
		"process.list": listOf(proto.Process{Def: procs.Def{Name: "queue"}, Status: procs.Failed, Message: "exited (code 1)"}),
	})
	var code int
	stdout(t, func() { code = Run([]string{"start", "queue", "--wait"}) })
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestRestartPrefersProcess(t *testing.T) {
	dir := projectFolder(t)
	var restarted atomic.Int32
	fakeSocket(t, fakeHost{
		"state":        stateOf(dir),
		"process.list": listOf(fixture...),
		"process.restart": func(json.RawMessage) (any, error) {
			restarted.Add(1)
			return proto.Process{Def: procs.Def{Name: "web"}, Status: procs.Starting}, nil
		},
	})
	var code int
	stdout(t, func() { code = Run([]string{"restart", "web"}) })
	if code != 0 || restarted.Load() != 1 {
		t.Errorf("exit %d, %d restarts; want 0 and 1", code, restarted.Load())
	}
}

func TestRestartOtherwiseMeansASession(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.list": listOf(fixture...)})
	// Not a process of the project, so restart looks for a session, and finds none.
	if code := Run([]string{"restart", "nosuch"}); code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
}

func TestCurrentProject(t *testing.T) {
	root := projectFolder(t)
	sub := filepath.Join(root, "sub", "deeper")
	other := filepath.Join(root, "other")
	for _, d := range []string{sub, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st := proto.State{
		Projects: []proto.Project{
			{ID: "outer", Dir: root},
			{ID: "inner", Dir: filepath.Join(root, "sub")},
		},
		Sessions: []proto.Session{{ID: "s1", Project: "outer"}, {ID: "s2", Project: "inner"}},
	}
	cases := []struct {
		name, session, cwd, want string
	}{
		{"session wins over the folder", "s2", root, "inner"},
		{"session in another project", "s1", sub, "outer"},
		{"longest folder", "", sub, "inner"},
		{"folder of the outer", "", other, "outer"},
		{"unknown session falls to the folder", "nope", other, "outer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("OMATERM_SESSION", c.session)
			t.Chdir(c.cwd)
			got, err := currentProject(st)
			if err != nil || got != c.want {
				t.Errorf("got %q %v, want %q", got, err, c.want)
			}
		})
	}
	t.Setenv("OMATERM_SESSION", "")
	t.Chdir(t.TempDir())
	if _, err := currentProject(st); err == nil || err.Error() != "not in an omaterm project" {
		t.Errorf("outside every project: %v", err)
	}
}

// A process that exits and is being restarted is not one to wait for: the
// exit is what the user needs to see.
func TestStartWaitSeesACrashLoop(t *testing.T) {
	dir := projectFolder(t)
	fakeSocket(t, fakeHost{
		"state": stateOf(dir),
		"process.start": func(json.RawMessage) (any, error) {
			return proto.Process{Def: procs.Def{Name: "bad"}, Status: procs.Starting}, nil
		},
		"process.list": listOf(proto.Process{Def: procs.Def{Name: "bad"}, Status: procs.Starting, Restarts: 1, LastExit: 1}),
	})
	path := os.Getenv("OMATERM_SOCKET")
	c, err := client.Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = processCmd(c, "process.start", []string{"bad", "--wait", "--timeout", "2s"})
	if err == nil || !strings.Contains(err.Error(), "exited (code 1)") {
		t.Fatalf("got %v, want the exit code", err)
	}
}

// omaterm key against an omaterm from before keys went by name sends the
// keys' plain bytes, as it did then.
func TestKeyFallsBackToBytes(t *testing.T) {
	var sent proto.SendParams
	fakeSocket(t, fakeHost{
		"state": func(json.RawMessage) (any, error) {
			return proto.State{Sessions: []proto.Session{{ID: "s1", Name: "w", Panes: []proto.Pane{{ID: "pa"}}}}}, nil
		},
		"key":  func(json.RawMessage) (any, error) { return nil, errors.New("no method key") },
		"send": func(p json.RawMessage) (any, error) { json.Unmarshal(p, &sent); return true, nil },
	})
	if code := Run([]string{"key", "w", "shift+tab", "enter"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if sent.ID != "pa" || sent.Data != "\x1b[Z\r" {
		t.Fatalf("sent %+v", sent)
	}
}

// Tailscale and Cloudflare as a test says they are, by the reason each
// can't be made, "" for one that can.
func withTunnels(t *testing.T, tailscale, cloudflare string) {
	t.Helper()
	old := availableTunnels
	availableTunnels = func() map[tunnel.Provider]string {
		return map[tunnel.Provider]string{tunnel.Tailscale: tailscale, tunnel.Cloudflare: cloudflare}
	}
	t.Cleanup(func() { availableTunnels = old })
}

// shareHost answers process.share with a process shared at url, and notes
// what it was asked.
func shareHost(dir, url string, asked *proto.ProcessParams) fakeHost {
	return fakeHost{"state": stateOf(dir),
		"process.share": func(raw json.RawMessage) (any, error) {
			json.Unmarshal(raw, asked)
			return proto.Process{Def: procs.Def{Name: asked.Name}, Share: url, ShareVia: asked.Via, Status: procs.Running}, nil
		},
		"process.unshare": func(raw json.RawMessage) (any, error) {
			json.Unmarshal(raw, asked)
			return proto.Process{}, nil
		}}
}

func TestShareChoosesTailscaleThenCloudflare(t *testing.T) {
	for _, tt := range []struct {
		name, tailscale, cloudflare string
		args                        []string
		via                         string // "" for an error
	}{
		{"both there", "", "", []string{"share", "web"}, "tailscale"},
		{"only cloudflare", "signed out", "", []string{"share", "web"}, "cloudflare"},
		{"asked for cloudflare", "", "", []string{"share", "web", "--cloudflare"}, "cloudflare"},
		{"asked for tailscale", "", "", []string{"share", "web", "--tailscale"}, "tailscale"},
		{"asked for what is missing", "signed out", "", []string{"share", "web", "--tailscale"}, ""},
		{"neither", "signed out", "not installed", []string{"share", "web"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := projectFolder(t)
			withTunnels(t, tt.tailscale, tt.cloudflare)
			var asked proto.ProcessParams
			fakeSocket(t, shareHost(dir, "https://x.example.net", &asked))
			var code int
			out := stdout(t, func() { code = Run(tt.args) })
			if tt.via == "" {
				if code == 0 || asked.Via != "" {
					t.Fatalf("exit %d, asked %+v", code, asked)
				}
				return
			}
			if code != 0 || asked.Via != tt.via || asked.Name != "web" || asked.Project != "p1" || strings.TrimSpace(out) != "https://x.example.net" {
				t.Fatalf("exit %d, asked %+v, out %q", code, asked, out)
			}
		})
	}
}

func TestShareOffAndUsage(t *testing.T) {
	dir := projectFolder(t)
	withTunnels(t, "", "")
	var asked proto.ProcessParams
	fakeSocket(t, shareHost(dir, "https://x.example.net", &asked))
	var code int
	out := stdout(t, func() { code = Run([]string{"share", "web", "--off"}) })
	if code != 0 || asked.Name != "web" || !strings.Contains(out, "no longer shared") {
		t.Fatalf("exit %d, asked %+v, out %q", code, asked, out)
	}
	for _, args := range [][]string{{"share"}, {"share", "a", "b"}, {"share", "web", "--tailscale", "--cloudflare"}, {"share", "web", "--off", "--cloudflare"}} {
		if code := Run(args); code == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
}

func TestShareOfAProcessThatIsNotUp(t *testing.T) {
	dir := projectFolder(t)
	withTunnels(t, "", "")
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.share": func(json.RawMessage) (any, error) { return proto.Process{}, nil }})
	out := stdout(t, func() { Run([]string{"share", "web"}) })
	if !strings.Contains(out, "once it is running") {
		t.Fatalf("out %q", out)
	}
}

func TestPsShowsTheShareWhereThereIsNoLink(t *testing.T) {
	dir := projectFolder(t)
	list := []proto.Process{{Def: procs.Def{Name: "api", Command: "x", Port: 3000}, Status: procs.Running, Share: "https://api.example.net", ShareVia: "cloudflare"}}
	fakeSocket(t, fakeHost{"state": stateOf(dir), "process.list": listOf(list...)})
	out := stdout(t, func() { Run([]string{"ps"}) })
	if !strings.Contains(out, "https://api.example.net") {
		t.Fatalf("ps: %q", out)
	}
	out = stdout(t, func() { Run([]string{"ps", "--json"}) })
	if !strings.Contains(out, `"share": "https://api.example.net"`) && !strings.Contains(out, `"share":"https://api.example.net"`) || !strings.Contains(out, "cloudflare") {
		t.Fatalf("ps --json: %q", out)
	}
}

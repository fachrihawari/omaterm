package cli

import (
	"context"
	"encoding/json"
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
	{Def: procs.Def{Name: "web", Command: "npm run dev", Port: 3000}, Status: procs.Running, PID: 4321},
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
		"web running 3000 4321 - -",
		"worker untrusted - - - worker isn't trusted yet: review it in omaterm",
		"queue failed - - 2 exited (code 1)",
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

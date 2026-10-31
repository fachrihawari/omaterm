//go:build linux || darwin

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/model"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

func processClient(t *testing.T, socket string) *client.Client {
	t.Helper()
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func writeProcesses(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, procs.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonProcessesTrustRunAndState(t *testing.T) {
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	writeProcesses(t, projectDir, "processes:\n  web:\n    command: \"sh -c 'echo up; sleep 30'\"\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
	if len(list) != 1 || list[0].Name != "web" || list[0].Trusted || list[0].Status != procs.Untrusted {
		t.Fatalf("process list: %+v", list)
	}
	if err := c.Call("process.start", proto.ProcessParams{Project: "p1", Name: "web"}, nil); err == nil || !strings.Contains(err.Error(), "isn't trusted yet") {
		t.Fatalf("untrusted start: %v", err)
	}
	call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: "web"})
	started := call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	if started.Status != procs.Starting && started.Status != procs.Running {
		t.Fatalf("start: %+v", started)
	}
	state := call[proto.State](t, c, "state", nil)
	var processSession *proto.Session
	for i := range state.Sessions {
		if state.Sessions[i].Name == "web" {
			processSession = &state.Sessions[i]
		}
	}
	if processSession == nil || processSession.Process != "web" || len(processSession.Panes) != 1 || processSession.Lead.ID != started.Pane {
		t.Fatalf("process session: %+v", state.Sessions)
	}
	if pr := processSession.Proc; pr == nil || pr.Name != "web" || !pr.Trusted || pr.Pane != started.Pane || pr.Session != processSession.ID {
		t.Fatalf("the session's process: %+v", pr)
	}
	waitFor(t, "process output", func() bool {
		got := call[proto.ReadResult](t, c, "read", proto.ReadParams{ID: started.Pane})
		return strings.Contains(got.Text, "up")
	})
	waitFor(t, "running process status", func() bool {
		list = call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
		return list[0].Status == procs.Running
	})
}

func TestDaemonProcessRestartStopAndEvent(t *testing.T) {
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	writeProcesses(t, projectDir, "processes:\n  bad:\n    command: \"sh -c 'exit 1'\"\n    restart: on-failure\n  long:\n    command: \"sleep 30\"\n    restart: always\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	for _, name := range []string{"bad", "long"} {
		call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: name})
	}
	d.procs.MaxRestarts = 2
	events := make(chan proto.Message, 32)
	unsub := d.Subscribe(func(m proto.Message) { events <- m })
	defer unsub()
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "bad"})
	waitFor(t, "paused process", func() bool {
		list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
		return len(list) == 2 && list[0].Status == procs.Paused
	})
	proc := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})[0]
	if proc.Restarts != 2 {
		t.Fatalf("restart count: %+v", proc)
	}
	found := false
	deadline := time.After(time.Second)
	for !found {
		select {
		case event := <-events:
			if event.Event == "process" && strings.Contains(string(event.Result), `"paused"`) {
				found = true
			}
		case <-deadline:
			t.Fatal("no paused process event")
		}
	}
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "long"})
	call[proto.Process](t, c, "process.stop", proto.ProcessParams{Project: "p1", Name: "long"})
	waitFor(t, "stopped process", func() bool {
		list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
		return list[1].Status == procs.Stopped
	})
	time.Sleep(100 * time.Millisecond)
	list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
	if list[1].Status != procs.Stopped || list[1].Restarts != 0 {
		t.Fatalf("stopped process restarted: %+v", list[1])
	}
}

func TestDaemonProcessAutostartSyncAndLocal(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	// A short folder, for the socket's path: macOS allows 103 bytes.
	root, err := os.MkdirTemp("", "omp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	projectDir := filepath.Join(root, "proj")
	if err := os.Mkdir(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auto := procs.Def{Name: "auto", Command: "echo auto; sleep 30", Autostart: true}
	trust := procs.OpenTrust(procs.TrustPath())
	if err := trust.Add(projectDir, auto.Variant()); err != nil {
		t.Fatal(err)
	}
	writeProcesses(t, projectDir, "processes:\n  auto:\n    command: \"echo auto; sleep 30\"\n    autostart: true\n  private:\n    command: \"sleep 30\"\n    autostart: true\n")
	stateFile := filepath.Join(root, "state.json")
	state := model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "proj", Dir: projectDir,
		Processes: []procs.Def{{Name: "local", Command: "sleep 30"}}}}}
	if err := model.Save(stateFile, state); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "daemon.sock")
	d, err := Start(Options{Socket: socket, StateDir: root, StateFile: stateFile, IdleExit: time.Minute, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	d.opts.Notifier = func(string, string, string) {}
	waitFor(t, "trusted autostart", func() bool {
		for _, p := range d.procs.List("p1") {
			if p.Name == "auto" && p.Pane != "" {
				return true
			}
		}
		return false
	})
	list := d.procs.List("p1")
	if len(list) != 3 || list[0].Name != "auto" || list[0].Pane == "" || list[1].Name != "private" || list[1].Pane != "" || list[1].Trusted || list[2].Name != "local" || !list[2].Local {
		t.Fatalf("autostart and local definitions: %+v", list)
	}
	writeProcesses(t, projectDir, "processes:\n  auto:\n    command: \"echo changed; sleep 30\"\n    autostart: true\n  private:\n    command: \"echo private; sleep 30\"\n    autostart: true\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	list = d.procs.List("p1")
	if !list[0].Changed || list[1].Trusted || list[1].Pane != "" {
		t.Fatalf("sync did not mark changed and untrusted definitions: %+v", list)
	}
}

//go:build linux || darwin

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

// TestDaemonProcessRestartsOnAChange drives the folder watch the daemon
// gives its processes: a file the definition's globs name is written, and
// the process starts again, in a new pane, saying which file did it.
func TestDaemonProcessRestartsOnAChange(t *testing.T) {
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	writeProcesses(t, projectDir, "processes:\n  web:\n    command: \"sh -c 'sleep 30'\"\n    watch: [\"*.txt\"]\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: "web"})
	started := call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})

	// The watch takes its first look when it starts, so a file already
	// there is not a change: this one is written after a settle.
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(projectDir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the process to restart on a change", func() bool {
		list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
		return len(list) == 1 && list[0].LastChange == "a.txt" && list[0].Pane != "" && list[0].Pane != started.Pane
	})
	list := call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"})
	if list[0].Status != procs.Starting && list[0].Status != procs.Running {
		t.Fatalf("the restarted process: %+v", list[0])
	}
	// Asked for on purpose, so it is no restart of a crashing process.
	if list[0].Restarts != 0 {
		t.Fatalf("a watch restart counted in the restarts: %+v", list[0])
	}
}

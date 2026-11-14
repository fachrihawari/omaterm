package daemon

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// A remote daemon gives a pane its own control socket, not the app's,
// which means nothing on that computer.
func TestRemoteDaemonGivesPanesItsSocket(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: filepath.Join(dir, "state.json"),
		Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	// The app says it is remote as it connects, as it would to a daemon a
	// login service started without the bridge's environment.
	r, err := ConnectRemote(Transport{Name: "test", Dial: func() (net.Conn, error) {
		return net.Dial("unix", socket)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if _, err := r.Start(session.StartOptions{ID: "p1", Exec: true,
		Command: []string{"sh", "-c", `printf '%s\n' "$OMATERM_SOCKET"; sleep 0.3`}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var text string
	for {
		if p := r.Get("p1"); p != nil {
			text = p.Text(false, 0)
			if strings.Contains(text, socket) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane's control socket is not %s: %q", socket, text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Without the app, a remote daemon can use the panes that run, but not
// make or list sessions: the app owns those.
func TestRemoteDaemonRefusesOfflineSessions(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: filepath.Join(dir, "state.json"),
		Version: "test", Remote: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	c, err := client.Dial(socket) // a control connection, but no app following it
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out any
	err = c.Call("state", nil, &out)
	if err == nil || proto.CodeOf(err) != proto.CodeAppClosed {
		t.Fatalf("state without the app: %v", err)
	}
}

package paths

import (
	"path/filepath"
	"testing"
)

// OMATERM_CONFIG_HOME moves the config on every OS, macOS included, whose
// own config folder ignores XDG_CONFIG_HOME.
func TestConfigHome(t *testing.T) {
	t.Setenv("OMATERM_DEV", "")
	dir := t.TempDir()
	t.Setenv("OMATERM_CONFIG_HOME", dir)
	if got, want := ConfigDir(), filepath.Join(dir, "omaterm"); got != want {
		t.Fatalf("ConfigDir() = %s, want %s", got, want)
	}
	if got, want := StateFile(), filepath.Join(dir, "omaterm", "state.json"); got != want {
		t.Errorf("StateFile() = %s, want %s", got, want)
	}
	t.Setenv("OMATERM_DEV", "1")
	if got, want := ConfigDir(), filepath.Join(dir, "omaterm-dev"); got != want {
		t.Errorf("a development build's ConfigDir() = %s, want %s", got, want)
	}
}

// OMATERM_DAEMON_SOCKET, which panes get, names the daemon's socket whatever
// the runtime directory is; unset, the runtime directory's is used.
func TestDaemonSocketOverride(t *testing.T) {
	run := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", run)
	t.Setenv("OMATERM_DAEMON_SOCKET", "")
	if p := DaemonSocketPath(); filepath.Dir(filepath.Dir(p)) != run {
		t.Fatalf("without the variable the socket is %s, want one in %s", p, run)
	}
	t.Setenv("OMATERM_DAEMON_SOCKET", "/elsewhere/d.sock")
	if p := DaemonSocketPath(); p != "/elsewhere/d.sock" {
		t.Errorf("DaemonSocketPath() = %s, want the variable's", p)
	}
}

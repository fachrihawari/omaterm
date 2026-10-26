package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacy(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "history"), 0o700)
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	for _, f := range []string{"workspace-v1.json", "workspace-recovery-v1.json", "history/master.key", "history/a.1.omhist", "sessions/p1.vt", "daemon.log"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)
	}
	if gone := RemoveLegacy(dir); len(gone) != 3 {
		t.Fatalf("removed %v, want the two workspace files and history/", gone)
	}
	for _, f := range []string{"workspace-v1.json", "workspace-recovery-v1.json", "history"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s left behind", f)
		}
	}
	for _, f := range []string{"sessions/p1.vt", "daemon.log"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s removed: it is today's", f)
		}
	}
	if gone := RemoveLegacy(dir); len(gone) != 0 {
		t.Errorf("a second run removed %v", gone)
	}
}

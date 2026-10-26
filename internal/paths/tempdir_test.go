//go:build unix

package paths

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A socket path too long for its place goes to a folder of this user's,
// not into the shared /tmp; a name already taken (here by a file) is
// passed over, and a folder of ours that others could open is closed.
func TestPrivateTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, strings.Repeat("long", 30)))
	taken := filepath.Join(tmp, Name()+"-"+strconv.Itoa(os.Getuid()))
	os.WriteFile(taken, nil, 0o644)
	p := DaemonSocketPath()
	dir := filepath.Dir(p)
	if dir == tmp || dir == taken {
		t.Fatalf("the socket goes to %s", p)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("its folder %s: %v %v", dir, st.Mode(), err)
	}
	os.Chmod(dir, 0o755)
	if q := DaemonSocketPath(); filepath.Dir(q) != dir {
		t.Fatalf("moved to %s", q)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Error("an opened-up folder wasn't closed again")
	}
}

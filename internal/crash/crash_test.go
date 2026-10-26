package crash

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain runs a crashing child when asked: the crash report must land in
// the log, which the parent then reads.
func TestMain(m *testing.M) {
	if dir := os.Getenv("CRASH_TEST_DIR"); dir != "" {
		Watch(dir, "app", "9.9.9")
		panic("crash test: on purpose")
	}
	os.Exit(m.Run())
}

func crashChild(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "CRASH_TEST_DIR="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("the child did not crash")
	}
}

func TestPanicLandsInTheLog(t *testing.T) {
	dir := t.TempDir()
	crashChild(t, dir)
	b, err := os.ReadFile(LogPath(dir, "app"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "panic: crash test: on purpose") {
		t.Fatalf("log has no panic:\n%s", b)
	}
	if got := Unseen(dir); len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("Unseen = %v, want the app's crash", got)
	}
	// The next run notes which run crashed, once.
	Watch(dir, "daemon", "test")
	note(dir, "app")
	note(dir, "app")
	b, _ = os.ReadFile(LogPath(dir, "app"))
	if n := strings.Count(string(b), "--- the crash above: omaterm app 9.9.9"); n != 1 {
		t.Fatalf("the run is noted %d times:\n%s", n, b)
	}
	MarkSeen(dir)
	if got := Unseen(dir); len(got) != 0 {
		t.Fatalf("after MarkSeen, Unseen = %v", got)
	}
}

func TestNoCrashNoReport(t *testing.T) {
	dir := t.TempDir()
	if got := Unseen(dir); len(got) != 0 {
		t.Fatalf("Unseen = %v on a fresh folder", got)
	}
}

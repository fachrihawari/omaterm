package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A title or body can't end the AppleScript string it goes in.
func TestAppleScriptString(t *testing.T) {
	for in, want := range map[string]string{
		"Approve deploy?":         `"Approve deploy?"`,
		`say "hi"`:                `"say \"hi\""`,
		`C:\dir\`:                 `"C:\\dir\\"`,
		`" & (do shell script "x`: `"\" & (do shell script \"x"`,
		"two\nlines\tand\x07bell": `"two\nlines\tand` + `bell"`,
		"café · ✓":                `"café · ✓"`,
		"":                        `""`,
	} {
		if got := appleScriptString(in); got != want {
			t.Errorf("appleScriptString(%q) = %s, want %s", in, got, want)
		}
	}
}

// On macOS the daemon notifies through osascript, with one script as an
// argument, and keeps quiet when the settings turn sound off.
func TestNotifyMac(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"notify":{"sound":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := make(chan []string, 1)
	old := runOsascript
	runOsascript = func(args ...string) error { ran <- args; return nil }
	t.Cleanup(func() { runOsascript = old })

	d := &Daemon{opts: Options{StateFile: filepath.Join(dir, "state.json")}}
	d.notifyMac(`deploy · "web"`, "Approve deploy?", "s1")
	want := []string{"-e", `display notification "Approve deploy?" with title "deploy · \"web\""`}
	if got := <-ran; !reflect.DeepEqual(got, want) {
		t.Fatalf("osascript %q, want %q", got, want)
	}

	os.Remove(filepath.Join(dir, "settings.json"))
	d.notifyMac("t", "b", "s1")
	if got := <-ran; got[1] != `display notification "b" with title "t" sound name "Glass"` {
		t.Fatalf("with sound: %q", got)
	}
}

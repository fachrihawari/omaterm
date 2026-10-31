package devurl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestElevateThroughPkexec(t *testing.T) {
	for _, tt := range []struct {
		exit int
		err  error
	}{{0, nil}, {126, ErrCancelled}, {127, ErrNoAgent}} {
		dir := t.TempDir()
		log := filepath.Join(dir, "log")
		script := "#!/bin/sh\necho \"$*\" > '" + log + "'\nexit " + strconv.Itoa(tt.exit) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		err := Elevate("linux", "/opt/omaterm", []string{"proxy", "setup", "--system"}, &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, tt.err) {
			t.Errorf("exit %d: %v, want %v", tt.exit, err, tt.err)
		}
		if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "/opt/omaterm proxy setup --system" {
			t.Errorf("pkexec was called with %q", b)
		}
	}
}

func TestElevateNoAgentByItsMessageAndOtherFailures(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	write("echo 'Error: No authentication agent found.' >&2; exit 1\n")
	if err := Elevate("linux", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, ErrNoAgent) {
		t.Errorf("the agent message: %v", err)
	}
	write("exit 3\n")
	if err := Elevate("linux", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || errors.Is(err, ErrNoAgent) || errors.Is(err, ErrCancelled) {
		t.Errorf("the step failing: %v", err)
	}
	os.Remove(filepath.Join(dir, "pkexec"))
	if err := Elevate("linux", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, ErrNoAgent) {
		t.Errorf("no pkexec: %v", err)
	}
}

func TestQuotingForMacOS(t *testing.T) {
	if got := shellJoin([]string{"/Applications/omaterm", "proxy", "it's"}); got != `'/Applications/omaterm' 'proxy' 'it'\''s'` {
		t.Errorf("shellJoin: %s", got)
	}
	if got := appleScriptString(`a "b" \c`); got != `"a \"b\" \\c"` {
		t.Errorf("appleScriptString: %s", got)
	}
}

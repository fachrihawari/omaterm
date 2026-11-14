package sshconn

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	got := quote("a'b c")
	want := `'a'\''b c'`
	if got != want {
		t.Fatalf("quote: %q, want %q", got, want)
	}
}

func TestRemoteCommandRunsTheBridge(t *testing.T) {
	t.Setenv("OMATERM_DEV", "")
	cmd := remoteCommand("")
	if !strings.HasPrefix(cmd, "sh -c '") {
		t.Fatalf("command doesn't run sh: %q", cmd)
	}
	for _, want := range []string{"__bridge", ".local/bin/omaterm", "command -v omaterm"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q: %q", want, cmd)
		}
	}
	if strings.Contains(cmd, "OMATERM_DEV") {
		t.Errorf("a release build asks for the dev paths: %q", cmd)
	}
}

func TestRemoteCommandChecksVersion(t *testing.T) {
	cmd := remoteCommand("0.14.5")
	for _, want := range []string{`"$p" version`, `"0.14.5"`, "__bridge"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q: %q", want, cmd)
		}
	}
	if cmd := remoteCommand("bad; rm -rf /"); strings.Contains(cmd, "rm -rf") {
		t.Fatalf("a version can reach the shell: %q", cmd)
	}
}

func TestRemoteCommandFindsDevBuild(t *testing.T) {
	t.Setenv("OMATERM_DEV", "1")
	cmd := remoteCommand("dev")
	for _, want := range []string{"export OMATERM_DEV=1", "omaterm-dev/bin/omaterm", "__bridge"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q: %q", want, cmd)
		}
	}
}

func TestSSHArgsAreUnattended(t *testing.T) {
	args := sshArgs()
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=15", "ServerAliveInterval=10", "ClearAllForwardings=yes"} {
		if !slices.Contains(args, want) {
			t.Errorf("ssh args lack %q: %v", want, args)
		}
	}
	if !slices.Contains(args, "-T") {
		t.Errorf("ssh args lack -T: %v", args)
	}
}

// A setup step runs on the host through the same ssh options as the
// bridge, and its output comes back.
func TestRunExecutesTheCommand(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nfor last; do :; done\nexec sh -c \"$last\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := Run("devbox", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Fatalf("output %q", out)
	}
}

// A bridge's connection is the ssh process's own standard input and
// output: what is written comes back, as the daemon's frames would.
func TestConnPlumbsTheBridge(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c, err := Dial("devbox")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello\n" {
		t.Fatalf("read %q", line)
	}
}

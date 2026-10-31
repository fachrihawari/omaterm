package cli

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/devurl"
)

// proxyFixture is a Linux machine in a temporary root with fake pkexec and
// certutil first, and alone, on the PATH, and a fake session daemon.
type proxyFixture struct {
	env   devurl.Env
	log   string
	calls []string // proxyCall's methods
}

func newProxyFixture(t *testing.T, pkexecExit int) *proxyFixture {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	f := &proxyFixture{log: filepath.Join(t.TempDir(), "calls.log")}
	scripts := map[string]string{
		"pkexec":                 "echo \"pkexec $*\" >> '" + f.log + "'\nexit " + itoa(pkexecExit) + "\n",
		"certutil":               "echo \"certutil $*\" >> '" + f.log + "'\n[ \"$3\" != -L ]\n", // -L: not there yet
		"update-ca-certificates": "echo update-ca-certificates >> '" + f.log + "'\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	f.env = devurl.Env{OS: "linux", Home: filepath.Join(root, "home"), Root: filepath.Join(root, "sys"),
		CADir:      filepath.Join(root, "home", "state", "devurl"),
		Geteuid:    func() int { return 1000 },
		SystemPool: func() (*x509.CertPool, error) { return x509.NewCertPool(), nil },
		LookupHost: func(string) ([]string, error) { return []string{"127.0.0.1"}, nil },
		Out:        &bytes.Buffer{}}
	for _, dir := range []string{f.env.Home, filepath.Join(f.env.Root, "usr/local/share/ca-certificates"), filepath.Join(f.env.Root, "proc/sys/net/ipv4")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.env.Root, "proc/sys/net/ipv4/ip_unprivileged_port_start"), []byte("1024\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := proxyCall
	proxyCall = func(method string, start bool) (daemon.ProxyStatus, error) {
		f.calls = append(f.calls, method)
		return daemon.ProxyStatus{Running: true, Addr: "127.0.0.1:443", Checks: []devurl.Check{{Name: "Proxy serving", OK: true, Detail: "127.0.0.1:443"}, {Name: "Trusted by the system", Detail: "not in the system's store"}},
			Routes: []devurl.Route{{Host: "shop.localhost", Project: "x", Process: "web", Port: 4001, Running: true}}}, nil
	}
	t.Cleanup(func() { proxyCall = old })
	return f
}

func itoa(n int) string { return strconv.Itoa(n) }

// throughPkexec skips a test of the system step's elevation off Linux:
// macOS elevates through osascript, which the fixture doesn't fake.
func throughPkexec(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the system step goes through pkexec on Linux only")
	}
}

func (f *proxyFixture) logged() string {
	b, _ := os.ReadFile(f.log)
	return string(b)
}

func TestProxySetupDryRunChangesNothing(t *testing.T) {
	f := newProxyFixture(t, 0)
	out := stdout(t, func() {
		if err := proxySetup(f.env, []string{"--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Setting up Dev URLs will:", "Make omaterm's certificate authority", "Trust it system-wide (update-ca-certificates) (asks for your password)", "ports 443 and up"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(f.env.CADir); err == nil {
		t.Error("a dry run made the authority")
	}
	if f.logged() != "" || len(f.calls) != 0 {
		t.Errorf("a dry run ran %q and asked the daemon %v", f.logged(), f.calls)
	}
}

func TestProxySetupRunsTheSystemStepThroughPkexecThenReloads(t *testing.T) {
	throughPkexec(t)
	f := newProxyFixture(t, 0)
	out := stdout(t, func() {
		if err := proxySetup(f.env, nil); err != nil {
			t.Fatal(err)
		}
	})
	exe, _ := selfPath()
	if want := "pkexec " + exe + " proxy setup --system --ca " + f.env.CAPath(); !strings.Contains(f.logged(), want) {
		t.Errorf("pkexec call %q not in %q", want, f.logged())
	}
	if _, err := os.Stat(f.env.CAPath()); err != nil {
		t.Errorf("no authority made before the system step: %v", err)
	}
	if len(f.calls) != 1 || f.calls[0] != "proxy.reload" {
		t.Errorf("daemon calls %v", f.calls)
	}
	if !strings.Contains(out, "✓ Proxy serving: 127.0.0.1:443") || !strings.Contains(out, "✗ Trusted by the system") {
		t.Errorf("checks missing:\n%s", out)
	}
}

func TestProxySetupWithoutAPolkitAgentSaysWhatToRun(t *testing.T) {
	throughPkexec(t)
	f := newProxyFixture(t, 127)
	var err error
	stdout(t, func() { err = proxySetup(f.env, nil) })
	exe, _ := selfPath()
	want := "sudo " + exe + " proxy setup --system --ca " + f.env.CAPath()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want it to say %q", err, want)
	}
	if len(f.calls) != 0 {
		t.Errorf("reloaded after a failed step: %v", f.calls)
	}
}

func TestProxySetupCancelled(t *testing.T) {
	throughPkexec(t)
	f := newProxyFixture(t, 126)
	var err error
	stdout(t, func() { err = proxySetup(f.env, nil) })
	if err == nil || !strings.Contains(err.Error(), "no password") {
		t.Fatalf("error %v", err)
	}
}

func TestProxySystemStepNeedsRootAndACertificate(t *testing.T) {
	f := newProxyFixture(t, 0)
	if err := proxySetup(f.env, []string{"--system"}); err == nil || !strings.Contains(err.Error(), "--ca") {
		t.Errorf("without --ca: %v", err)
	}
	if err := proxySetup(f.env, []string{"--system", "--ca", "/nonexistent.pem"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("without root: %v", err)
	}
	if err := proxyUninstall(f.env, []string{"--system"}); err == nil || !strings.Contains(err.Error(), "--ca") {
		t.Errorf("uninstall without --ca: %v", err)
	}
}

func TestProxyUninstallThroughPkexecThenRemovesTheFiles(t *testing.T) {
	throughPkexec(t)
	f := newProxyFixture(t, 0)
	stdout(t, func() {
		if err := proxySetup(f.env, nil); err != nil {
			t.Fatal(err)
		}
	})
	// The fake pkexec changed nothing: put what the system step leaves.
	anchor := filepath.Join(f.env.Root, "usr/local/share/ca-certificates/omaterm-dev-ca.crt")
	if err := os.WriteFile(anchor, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := stdout(t, func() {
		if err := proxyUninstall(f.env, nil); err != nil {
			t.Fatal(err)
		}
	})
	exe, _ := selfPath()
	if want := "pkexec " + exe + " proxy uninstall --system --ca " + f.env.CAPath(); !strings.Contains(f.logged(), want) {
		t.Errorf("pkexec call %q not in %q", want, f.logged())
	}
	if _, err := os.Stat(f.env.CAPath()); err == nil {
		t.Error("the authority is still there")
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output:\n%s", out)
	}
}

func TestProxyUninstallKeepsTheAuthorityWhenTheSystemStepFails(t *testing.T) {
	throughPkexec(t)
	f := newProxyFixture(t, 127)
	stdout(t, func() { _ = proxySetup(f.env, nil) }) // makes the authority, then no agent
	anchor := filepath.Join(f.env.Root, "usr/local/share/ca-certificates/omaterm-dev-ca.crt")
	if err := os.WriteFile(anchor, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	stdout(t, func() { err = proxyUninstall(f.env, nil) })
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("error %v", err)
	}
	if _, statErr := os.Stat(f.env.CAPath()); statErr != nil {
		t.Errorf("the authority was removed with the system part in place: %v", statErr)
	}
}

func TestProxyStatusJSONAndText(t *testing.T) {
	f := newProxyFixture(t, 0)
	var st daemon.ProxyStatus
	out := stdout(t, func() {
		if err := proxyStatus(f.env, []string{"--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Running || len(st.Checks) != 2 || len(st.Routes) != 1 {
		t.Fatalf("%v: %s", err, out)
	}
	out = stdout(t, func() {
		if err := proxyStatus(f.env, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"✓ Proxy serving: 127.0.0.1:443", "✗ Trusted by the system: not in the system's store", "shop.localhost", "web in x (running, port 4001)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestProxyStatusWithNoDaemonRunsTheChecksItself(t *testing.T) {
	f := newProxyFixture(t, 0)
	proxyCall = func(string, bool) (daemon.ProxyStatus, error) { return daemon.ProxyStatus{}, client.ErrUnreachable }
	out := stdout(t, func() {
		if err := proxyStatus(f.env, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "✗ Proxy serving: the session daemon isn't running") || !strings.Contains(out, "✗ Certificate authority made") {
		t.Errorf("status:\n%s", out)
	}
}

func TestProxyCommandIsKnown(t *testing.T) {
	if !IsCommand("proxy") || !strings.Contains(helpText, "omaterm proxy setup") {
		t.Fatal("proxy isn't a command or isn't in the help")
	}
	if err := proxyCmd([]string{"nope"}); err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
}

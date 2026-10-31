//go:build linux || darwin

package tunnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakePATH puts fake commands on PATH, alone: a test that leaves one
// out asks for it to be missing, without the real one answering. The
// fakes run absolute commands, as PATH holds nothing else.
func fakePATH(t *testing.T, commands map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	return dir
}

// fakeCloudflared writes the trycloudflare message cloudflared logs,
// stays up, and notes its pid for the test.
const fakeCloudflared = `echo $$ > "$CF_PID"
echo "INF |  https://some-words-here.trycloudflare.com |" >&2
exec /bin/sleep 300`

// fakeTailscale answers status from a file and logs what it is told to
// serve, exiting 0 as --bg does.
const fakeTailscale = `if [ "$1" = status ]; then
	printf '%s' "$TS_STATUS"
	exit 0
fi
if [ "$1" = serve ] && [ "$2" = status ]; then
	if [ "$TS_SERVE_FAIL" = 1 ]; then exit 1; fi
	printf '%s' "${TS_SERVE:-{\}}"
	exit 0
fi
printf '%s\n' "$*" >> "$TS_LOG"
exit 0`

// serveStatus is a tailscale status --json answer, signed in as
// box.tailnet.ts.net.
func serveStatus(backend string) string {
	return `{"BackendState":"` + backend + `","Self":{"DNSName":"box.tailnet.ts.net."}}`
}

// tunnelTest sets the fake commands up with a place for their notes.
func tunnelTest(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("CF_PID", filepath.Join(dir, "cf.pid"))
	t.Setenv("TS_LOG", filepath.Join(dir, "tailscale.log"))
	t.Setenv("TS_STATUS", serveStatus("Running"))
	return dir
}

// A quick tunnel says its URL, stays up, and ends with its whole
// process group when stopped.
func TestCloudflareTunnel(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": fakeCloudflared})
	tun, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370", Name: "web access"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://some-words-here.trycloudflare.com" {
		t.Fatalf("URL %q", got)
	}
	select {
	case <-tun.Done():
		t.Fatal("the tunnel ended before it was stopped")
	default:
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tun.Done():
	default:
		t.Fatal("no end after Stop")
	}
	if tun.Err() != nil {
		t.Fatalf("a stopped tunnel is no failure: %v", tun.Err())
	}
	// The command itself ended: its pid answers no probe.
	b, err := os.ReadFile(filepath.Join(dir, "cf.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatal("cloudflared still runs after Stop")
	}
}

// A cloudflared that fails says why at once, rather than waiting out
// the start.
func TestCloudflareTunnelFails(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": `echo "ERR cloudflared: no network" >&2
exit 1`})
	_, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370"})
	if err == nil || !strings.Contains(err.Error(), "cloudflared") {
		t.Fatalf("no failure: %v", err)
	}
}

// A cloudflared that never says a URL is given up on, and ended.
func TestCloudflareTunnelNoURL(t *testing.T) {
	undo := shortStart(t, 200*time.Millisecond)
	defer undo()
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": `exec /bin/sleep 300`})
	_, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370"})
	if err == nil || !strings.Contains(err.Error(), "no URL") {
		t.Fatalf("no failure: %v", err)
	}
}

// Without cloudflared there is no quick tunnel, and Start says so with
// the reason Available gives.
func TestCloudflareNotInstalled(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{})
	_, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370"})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "cloudflared is not installed") {
		t.Fatalf("missing cloudflared: %v", err)
	}
	if why := Available()[Cloudflare]; why != "cloudflared is not installed" {
		t.Fatalf("Available: %q", why)
	}
}

// tailscale serve runs in the background and is turned off by its
// https port; the URL is this machine's tailnet name.
func TestTailscaleTunnel(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	tun, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370", Name: "web access"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://box.tailnet.ts.net" {
		t.Fatalf("URL %q", got)
	}
	select {
	case <-tun.Done():
		t.Fatal("the tunnel ended before it was stopped")
	default:
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tun.Done():
	default:
		t.Fatal("no end after Stop")
	}
	served, err := os.ReadFile(filepath.Join(dir, "tailscale.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := "serve --bg --https=443 http://127.0.0.1:7370\nserve --https=443 off\n"
	if string(served) != want {
		t.Fatalf("tailscale ran %q, want %q", served, want)
	}
}

// A serve on another port says so in its URL and is turned off by that
// port, as a share's is.
func TestTailscaleTunnelPort(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	tun, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:8370", Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://box.tailnet.ts.net:8443" {
		t.Fatalf("URL %q", got)
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	served, _ := os.ReadFile(filepath.Join(dir, "tailscale.log"))
	want := "serve --bg --https=8443 http://127.0.0.1:8370\nserve --https=8443 off\n"
	if string(served) != want {
		t.Fatalf("tailscale ran %q, want %q", served, want)
	}
}

// Signed out, nothing is served at all.
func TestTailscaleSignedOut(t *testing.T) {
	dir := tunnelTest(t)
	t.Setenv("TS_STATUS", serveStatus("NeedsLogin"))
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	_, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370"})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "signed out") {
		t.Fatalf("signed out: %v", err)
	}
	if why := Available()[Tailscale]; why != "Tailscale is signed out" {
		t.Fatalf("Available: %q", why)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tailscale.log")); err == nil && len(b) > 0 {
		t.Fatal("tailscale served while signed out")
	}
}

// Without tailscale there is no serve.
func TestTailscaleNotInstalled(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{})
	_, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370"})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "tailscale is not installed") {
		t.Fatalf("missing tailscale: %v", err)
	}
	if why := Available()[Tailscale]; why != "tailscale is not installed" {
		t.Fatalf("Available: %q", why)
	}
}

// Both providers in place, Available says nothing stands in the way.
func TestAvailable(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": fakeCloudflared, "tailscale": fakeTailscale})
	why := Available()
	if why[Cloudflare] != "" || why[Tailscale] != "" {
		t.Fatalf("available: %+v", why)
	}
}

// shortStart shortens the start's wait, until undone.
func shortStart(t *testing.T, d time.Duration) (undo func()) {
	t.Helper()
	was := startTimeout
	startTimeout = d
	return func() { startTimeout = was }
}

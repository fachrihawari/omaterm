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

// fakeNamedCloudflared is cloudflared running a named tunnel: it wants
// the token in its environment and nowhere in its arguments, notes those
// and its pid for the test, says the connection is up, and stays up.
const fakeNamedCloudflared = `if [ -z "$TUNNEL_TOKEN" ]; then echo "ERR no TUNNEL_TOKEN" >&2; exit 1; fi
case "$*" in *"$TUNNEL_TOKEN"*) echo "ERR the token is in the arguments" >&2; exit 1;; esac
echo $$ > "$CF_PID"
printf '%s\n' "$*" > "$CF_ARGS"
echo "2026-11-05T10:00:00Z INF Starting tunnel tunnelID=abc" >&2
echo "2026-11-05T10:00:01Z INF Registered tunnel connection connIndex=0 location=sin01 protocol=quic" >&2
exec /bin/sleep 300`

// fakeTailscale answers status from a file and logs what it is told to
// serve, exiting 0 as --bg does. TS_FUNNEL_OFF has funnel print the link
// that turns Funnel on, and then wait for it ("wait") or end ("exit"), as
// Tailscale does when the tailnet hasn't turned it on.
const fakeTailscale = `if [ "$1" = funnel ] && [ "$2" = --bg ] && [ -n "$TS_FUNNEL_OFF" ]; then
	printf '\nFunnel is not enabled on your tailnet.\nTo enable, visit:\n\n         https://login.tailscale.com/f/funnel?node=nABC123\n\n'
	if [ "$TS_FUNNEL_OFF" = wait ]; then exec /bin/sleep 300; fi
	exit 0
fi
if [ "$1" = status ]; then
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
	t.Setenv("CF_ARGS", filepath.Join(dir, "cf.args"))
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
	if why := Available()[Funnel]; why != "Tailscale is signed out" {
		t.Fatalf("Available, Funnel: %q", why)
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
	if why := Available()[Funnel]; why != "tailscale is not installed" {
		t.Fatalf("Available, Funnel: %q", why)
	}
}

// Both providers in place, Available says nothing stands in the way.
func TestAvailable(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": fakeCloudflared, "tailscale": fakeTailscale})
	why := Available()
	if why[Cloudflare] != "" || why[Tailscale] != "" || why[Funnel] != "" {
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

// Only a tunnel's own subdomain is its URL: cloudflared names Cloudflare's
// api host when it fails to ask for a tunnel.
func TestQuickURL(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"INF |  https://some-words-here.trycloudflare.com |", "https://some-words-here.trycloudflare.com"},
		{`ERR Post "https://api.trycloudflare.com/tunnel": dial tcp: no such host`, ""},
		{"https://api.trycloudflare.com then https://real-one.trycloudflare.com", "https://real-one.trycloudflare.com"},
		{"https://api-x.trycloudflare.com", "https://api-x.trycloudflare.com"},
		{"no url here", ""},
	} {
		if got := quickURL(tc.line); got != tc.want {
			t.Errorf("quickURL(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// A cloudflared that fails with the api host in its log is a failure, not
// a tunnel at that address.
func TestCloudflareTunnelFailureNamesAPI(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": `echo 'ERR Failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": dial tcp: lookup api.trycloudflare.com: no such host' >&2
exit 1`})
	tun, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370"})
	if err == nil {
		tun.Stop()
		t.Fatalf("a tunnel at %q from a failure line", tun.URL())
	}
}

// A helper that keeps cloudflared's log open after it ended does not keep
// the tunnel from ending, and stopping an ended tunnel is quiet.
func TestCloudflareTunnelEndsWithAHelperOnItsLog(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{
		"sleep": "exec /bin/sleep \"$@\"",
		"cloudflared": `echo "INF |  https://some-words-here.trycloudflare.com |" >&2
sleep 5 &
exit 0`,
	})
	tun, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tun.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the tunnel did not end though cloudflared did")
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
}

// A serve that goes away under a running tunnel ends it with an error, and
// a status that cannot be read does not.
func TestTailscaleTunnelEndsWhenItsServeIsGone(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	old := serveWatch
	serveWatch = 40 * time.Millisecond
	defer func() { serveWatch = old }()

	const target = "http://127.0.0.1:7370"
	t.Setenv("TS_SERVE", `{"TCP":{"443":{}},"Web":{"box.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"`+target+`"}}}}}`)
	tun, err := Start(context.Background(), Spec{Provider: Tailscale, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	t.Setenv("TS_SERVE_FAIL", "1")
	select {
	case <-tun.Done():
		t.Fatal("the tunnel ended on a status that could not be read")
	case <-time.After(300 * time.Millisecond):
	}

	t.Setenv("TS_SERVE_FAIL", "")
	t.Setenv("TS_SERVE", `{}`)
	select {
	case <-tun.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the tunnel did not end though its serve was reset")
	}
	if tun.Err() == nil || !strings.Contains(tun.Err().Error(), "no longer serves") {
		t.Fatalf("Err: %v", tun.Err())
	}
}

// testToken stands for a named tunnel's token, which is a secret.
const testToken = "eyJhIjoiYWNjb3VudCIsInQiOiJ0dW5uZWwiLCJzIjoic2VjcmV0In0="

// A named tunnel runs with its token in cloudflared's environment, not its
// arguments, is up once cloudflared registers its connection, is reached
// at the user's hostname, and ends with its process group when stopped.
func TestCloudflareNamedTunnel(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": fakeNamedCloudflared})
	tun, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370", Name: "web access",
		Token: testToken, Hostname: "term.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://term.example.com" {
		t.Fatalf("URL %q", got)
	}
	args, err := os.ReadFile(filepath.Join(dir, "cf.args"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(args); got != "tunnel --no-autoupdate run\n" {
		t.Fatalf("cloudflared ran with %q", got)
	}
	select {
	case <-tun.Done():
		t.Fatal("the tunnel ended before it was stopped")
	default:
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	if tun.Err() != nil {
		t.Fatalf("a stopped tunnel is no failure: %v", tun.Err())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "cf.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 || syscall.Kill(pid, 0) == nil {
		t.Fatalf("cloudflared (pid %d) still runs after Stop", pid)
	}
}

// A token without the hostname it serves is refused before cloudflared
// runs: there would be no URL to give.
func TestCloudflareTokenWithoutHostname(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": fakeNamedCloudflared})
	_, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370", Token: testToken})
	if err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("a token alone: %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the error says the token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cf.pid")); err == nil {
		t.Fatal("cloudflared ran")
	}
}

// A named tunnel that fails says cloudflared's first error, and never the
// token, even when cloudflared's log does.
func TestCloudflareNamedTunnelFailsWithoutTheToken(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"cloudflared": `echo "2026-11-05T10:00:00Z INF Starting tunnel" >&2
echo "2026-11-05T10:00:00Z ERR Provided Tunnel token is not valid: $TUNNEL_TOKEN" >&2
echo "2026-11-05T10:00:00Z ERR a later error" >&2
exit 1`})
	_, err := Start(context.Background(), Spec{Provider: Cloudflare, Target: "http://127.0.0.1:7370",
		Token: testToken, Hostname: "term.example.com"})
	if err == nil || !strings.Contains(err.Error(), "cloudflared: Provided Tunnel token is not valid") {
		t.Fatalf("no failure, or not cloudflared's: %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the error says the token: %v", err)
	}
}

// Funnel serves in the background on the tailnet name, open to the
// internet, and is turned off as a funnel.
func TestFunnelTunnel(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	tun, err := Start(context.Background(), Spec{Provider: Funnel, Target: "http://127.0.0.1:7370", Name: "web access"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://box.tailnet.ts.net" {
		t.Fatalf("URL %q", got)
	}
	if err := tun.Stop(); err != nil {
		t.Fatal(err)
	}
	served, _ := os.ReadFile(filepath.Join(dir, "tailscale.log"))
	want := "funnel --bg --https=443 http://127.0.0.1:7370\nfunnel --https=443 off\n"
	if string(served) != want {
		t.Fatalf("tailscale ran %q, want %q", served, want)
	}
}

// Funnel takes 443, 8443 and 10000 alone: another port is refused before
// tailscale is told anything.
func TestFunnelPorts(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	tun, err := Start(context.Background(), Spec{Provider: Funnel, Target: "http://127.0.0.1:7370", Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if got := tun.URL(); got != "https://box.tailnet.ts.net:8443" {
		t.Fatalf("URL %q", got)
	}
	tun.Stop()
	os.Remove(filepath.Join(dir, "tailscale.log"))
	for _, port := range []int{8080, 9443, 10001} {
		_, err := Start(context.Background(), Spec{Provider: Funnel, Target: "http://127.0.0.1:7370", Port: port})
		if err == nil || !strings.Contains(err.Error(), "443, 8443 or 10000") {
			t.Fatalf("port %d: %v", port, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tailscale.log")); len(b) != 0 {
		t.Fatalf("tailscale was told to serve: %q", b)
	}
}

// A tailnet without Funnel turned on has tailscale print the link that
// turns it on, and wait for the user, or end. Start ends it at once and
// fails with the link, rather than waiting out the serve's 30 seconds.
func TestFunnelNotTurnedOn(t *testing.T) {
	for _, mode := range []string{"wait", "exit"} {
		t.Run(mode, func(t *testing.T) {
			tunnelTest(t)
			t.Setenv("TS_FUNNEL_OFF", mode)
			fakePATH(t, map[string]string{"tailscale": fakeTailscale})
			began := time.Now()
			tun, err := Start(context.Background(), Spec{Provider: Funnel, Target: "http://127.0.0.1:7370"})
			if err == nil {
				tun.Stop()
				t.Fatal("a funnel the tailnet hasn't turned on started")
			}
			if took := time.Since(began); took > 5*time.Second {
				t.Fatalf("it took %s to fail", took)
			}
			want := "Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=nABC123"
			if err.Error() != want {
				t.Fatalf("error %q, want %q", err, want)
			}
			if got := EnableLink(err.Error()); got != "https://login.tailscale.com/f/funnel?node=nABC123" {
				t.Fatalf("EnableLink: %q", got)
			}
		})
	}
}

// tailscale serve without HTTPS turned on prints its own link, and is
// said so too.
func TestServeHTTPSNotTurnedOn(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": `if [ "$1" = status ]; then printf '%s' "$TS_STATUS"; exit 0; fi
if [ "$2" = status ]; then printf '{}'; exit 0; fi
echo "To enable, visit: https://login.tailscale.com/f/serve?node=nABC123"
exec /bin/sleep 300`})
	_, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370"})
	if err == nil || err.Error() != "Turn on HTTPS for this tailnet: https://login.tailscale.com/f/serve?node=nABC123" {
		t.Fatalf("error %v", err)
	}
}

func TestEnableLink(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=n1", "https://login.tailscale.com/f/funnel?node=n1"},
		{"tailscale serve: exit status 1", ""},
		{"see https://login.tailscale.com/admin", ""},
	} {
		if got := EnableLink(tc.in); got != tc.want {
			t.Errorf("EnableLink(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A hostname is taken bare, or from its URL pasted whole; a path, a port
// or a name that isn't one is refused.
func TestParseHostname(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"term.example.com", "term.example.com"},
		{"  Term.Example.com ", "term.example.com"},
		{"https://term.example.com/", "term.example.com"},
		{"https://term.example.com", "term.example.com"},
		{"term.example.com/", "term.example.com"},
		{"", ""},
		{"https://", ""},
	} {
		if got, err := ParseHostname(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseHostname(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"https://term.example.com/app", "term.example.com:8443", "ftp://term.example.com",
		"localhost", "term example.com", "-term.example.com", "term..example.com", "user@term.example.com",
	} {
		if got, err := ParseHostname(bad); !errors.Is(err, ErrHostname) {
			t.Errorf("ParseHostname(%q) = %q, %v; want refused", bad, got, err)
		}
	}
}

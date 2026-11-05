package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omaterm/internal/ctl"
	"omaterm/internal/paths"
	"omaterm/internal/web"
)

// tunnelOf takes --tunnel's provider, "none" turning it off, and refuses
// anything else before a thing is changed.
func TestTunnelOf(t *testing.T) {
	flags := func(args ...string) *flags {
		f, err := parse(args, map[string]string{"tunnel": "tunnel"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"--tunnel", "none"}, ""},
		{[]string{"--tunnel=none"}, ""},
		{[]string{"--tunnel", "tailscale"}, "tailscale"},
		{[]string{"--tunnel", "cloudflare"}, "cloudflare"},
		{[]string{"--tunnel", "funnel"}, "funnel"},
	} {
		got, err := tunnelOf(flags(c.args...))
		if err != nil || got != c.want {
			t.Errorf("%v: %q %v, want %q", c.args, got, err, c.want)
		}
	}
	if _, err := tunnelOf(flags("--tunnel", "warp")); !errors.Is(err, errUsage) {
		t.Errorf("an unknown tunnel: %v", err)
	}
	// web on takes it among its other flags.
	f, err := parse([]string{"--network", "--tunnel", "tailscale", "--port", "7400"},
		map[string]string{"port": "port", "tunnel": "tunnel"}, map[string]string{"network": "network"})
	if err != nil || f.vals["tunnel"] != "tailscale" || !f.set["network"] {
		t.Fatalf("web on flags: %+v %v", f, err)
	}
}

// testToken stands for a Cloudflare tunnel's token, a secret.
const testToken = "eyJhIjoiYWNjb3VudCIsInQiOiJ0dW5uZWwiLCJzIjoic2VjcmV0In0="

// --hostname takes a bare host, or its URL pasted whole; --token takes the
// token, or - for one line of stdin.
func TestCloudflareOf(t *testing.T) {
	of := func(stdin string, args ...string) (cloudflareFlags, error) {
		f, err := parse(args, map[string]string{"hostname": "hostname", "token": "token"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cloudflareOf(f, strings.NewReader(stdin))
	}
	cf, err := of(" "+testToken+"  \nthe next line\n", "--hostname", "https://Term.Example.com/", "--token", "-")
	if err != nil || cf != (cloudflareFlags{host: "term.example.com", token: testToken, hostSet: true, tokenSet: true}) {
		t.Fatalf("%+v %v", cf, err)
	}
	// Without a newline at its end, as echo -n gives.
	if cf, err := of(testToken, "--token", "-"); err != nil || cf.token != testToken || cf.hostSet {
		t.Fatalf("stdin without a newline: %+v %v", cf, err)
	}
	if cf, err := of("", "--token", testToken); err != nil || cf.token != testToken {
		t.Fatalf("the token as given: %+v %v", cf, err)
	}
	if cf, err := of("", "--hostname", ""); err != nil || !cf.hostSet || cf.host != "" || cf.tokenSet {
		t.Fatalf(`--hostname "": %+v %v`, cf, err)
	}
	if cf, err := of(""); err != nil || cf != (cloudflareFlags{}) {
		t.Fatalf("no flags: %+v %v", cf, err)
	}
	for _, bad := range [][]string{
		{"--hostname", "https://term.example.com/app"},
		{"--hostname", "term.example.com:8443"},
		{"--token", ""},
		{"--token", "-"}, // nothing on stdin
		{"--hostname", "", "--token", testToken},
	} {
		_, err := of("", bad...)
		if !errors.Is(err, errUsage) {
			t.Errorf("%q: %v", bad, err)
		}
		if err != nil && strings.Contains(err.Error(), testToken) {
			t.Errorf("%q: the error says the token", bad)
		}
	}
}

// webTest points omaterm at a config of its own and a session daemon that
// answers web.reload with st, and keeps tailscale and cloudflared off PATH.
func webTest(t *testing.T, st web.Status) *web.Store {
	t.Helper()
	t.Setenv("OMATERM_CONFIG_HOME", t.TempDir())
	t.Setenv("OMATERM_DEV", "")
	t.Setenv("PATH", t.TempDir())
	// A short folder, for the socket's path.
	run, err := os.MkdirTemp("", "omw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	t.Setenv("XDG_RUNTIME_DIR", run)
	os.MkdirAll(filepath.Dir(paths.DaemonSocketPath()), 0o700)
	srv, err := ctl.Listen(paths.DaemonSocketPath(), fakeHost{
		"web.reload": func(json.RawMessage) (any, error) { return st, nil },
		"web.status": func(json.RawMessage) (any, error) { return st, nil },
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return web.OpenStore(web.StorePath(paths.ConfigDir()))
}

// web on keeps the user's own Cloudflare tunnel, read from stdin and
// picking Cloudflare; nothing the command prints says the token, and
// --hostname "" goes back to a quick tunnel, forgetting it.
func TestWebOnCloudflareDomain(t *testing.T) {
	store := webTest(t, web.Status{On: true, Addr: "127.0.0.1:7370", TunnelURL: "https://term.example.com"})
	var out string
	var err error
	withStdin(t, testToken+"\n", func() {
		out = stdout(t, func() {
			err = webCmd([]string{"on", "--hostname", "https://term.example.com/", "--token", "-"})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.Config()
	if cfg.Tunnel != "cloudflare" || cfg.CloudflareHost != "term.example.com" || cfg.CloudflareToken != testToken {
		t.Fatalf("saved %+v", cfg)
	}
	if !strings.Contains(out, "tunnel (cloudflare): https://term.example.com") || strings.Contains(out, "quick tunnel") {
		t.Fatalf("web on said %q", out)
	}
	for _, args := range [][]string{{"status"}, {"tunnel"}, {"pair"}} {
		got := stdout(t, func() { webCmd(args) })
		out += got
		if args[0] == "status" && !strings.Contains(got, "https://term.example.com") {
			t.Fatalf("status said %q", got)
		}
	}
	if strings.Contains(out, testToken) {
		t.Fatalf("the token was printed: %q", out)
	}

	// Another tunnel with a domain is refused, before anything is saved.
	if err := webCmd([]string{"on", "--tunnel", "tailscale", "--hostname", "web.example.com"}); !errors.Is(err, errUsage) {
		t.Fatalf("tailscale with a domain: %v", err)
	}
	if cfg, _ := store.Config(); cfg.CloudflareHost != "term.example.com" {
		t.Fatalf("saved anyway: %+v", cfg)
	}

	out = stdout(t, func() { err = webCmd([]string{"on", "--hostname", ""}) })
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = store.Config()
	if cfg.Tunnel != "cloudflare" || cfg.CloudflareHost != "" || cfg.CloudflareToken != "" {
		t.Fatalf("not back to a quick tunnel: %+v", cfg)
	}
	if !strings.Contains(out, "quick tunnel's link changes each time it starts") {
		t.Fatalf("no word of the link changing: %q", out)
	}
}

// A token with no hostname, given or kept, is refused: the tunnel would
// have no link to give.
func TestWebOnTokenNeedsHostname(t *testing.T) {
	store := webTest(t, web.Status{On: true, Addr: "127.0.0.1:7370"})
	err := webCmd([]string{"on", "--tunnel", "cloudflare", "--token", testToken})
	if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "--hostname") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("a token alone: %v", err)
	}
	if cfg, _ := store.Config(); cfg.CloudflareToken != "" || cfg.Enabled {
		t.Fatalf("saved anyway: %+v", cfg)
	}
}

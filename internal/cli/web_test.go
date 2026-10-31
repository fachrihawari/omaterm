package cli

import (
	"errors"
	"testing"
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

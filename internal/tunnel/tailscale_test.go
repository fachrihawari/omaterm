package tunnel

import (
	"errors"
	"os"
	"testing"
)

// TestMain keeps the tests off a Mac's real Tailscale app: a test that
// leaves tailscale off PATH asks for it to be missing, and the app's
// command would make real serves.
func TestMain(m *testing.M) {
	tailscaleApp = os.DevNull + "-Tailscale.app"
	os.Exit(m.Run())
}

func TestTailscaleBinFor(t *testing.T) {
	onPath := func(string) (string, error) { return "/opt/homebrew/bin/tailscale", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	app := func(p string) bool { return p == tailscaleApp }
	none := func(string) bool { return false }
	cases := []struct {
		goos   string
		look   func(string) (string, error)
		exists func(string) bool
		want   string
	}{
		{"darwin", onPath, app, "/opt/homebrew/bin/tailscale"}, // PATH first
		{"darwin", missing, app, tailscaleApp},
		{"darwin", missing, none, "tailscale"},
		{"linux", missing, app, "tailscale"}, // the app is macOS's alone
	}
	for _, c := range cases {
		if got := tailscaleBinFor(c.goos, c.look, c.exists); got != c.want {
			t.Errorf("%s: %q, want %q", c.goos, got, c.want)
		}
	}
}

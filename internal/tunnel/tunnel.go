// Package tunnel reaches a local address from elsewhere through a tunnel
// the user already has: Tailscale (their own devices, on their tailnet),
// Tailscale Funnel (a public https URL on this machine's tailnet name), or
// Cloudflare (a public https URL: a quick tunnel's, new each time it
// starts, or the user's own domain through a tunnel they made). omaterm
// runs the tunnel's command itself, reads the URL it gets, and stops it.
// Web access uses it for its page, and Dev URLs to share a process.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider is a kind of tunnel.
type Provider string

const (
	Tailscale  Provider = "tailscale"  // tailscale serve: the tailnet only
	Funnel     Provider = "funnel"     // tailscale funnel: public
	Cloudflare Provider = "cloudflare" // cloudflared: public
)

// Spec is a tunnel to make.
type Spec struct {
	Provider Provider
	// Target is the local address to reach, as http://127.0.0.1:7370.
	Target string
	// Name says what the tunnel is for, in logs and errors: "web access",
	// "shop".
	Name string
	// Port is the https port Tailscale serves it on: 0 is 443. A share
	// takes another (8443, 10000…), as web access may hold 443. Funnel
	// takes only 443, 8443 and 10000.
	Port int
	// Token is a Cloudflare named tunnel's, which the user made in
	// Cloudflare's dashboard to reach Target at Hostname: empty for a
	// quick tunnel. It is a secret: cloudflared gets it in its
	// environment, and nothing says it.
	Token string
	// Hostname is the named tunnel's public name, as term.example.com.
	Hostname string
}

// Tunnel is a running tunnel.
type Tunnel interface {
	// URL is where the target is reached: https://box.tailnet.ts.net,
	// https://some-words.trycloudflare.com, https://term.example.com.
	URL() string
	// Err is why the tunnel ended, once Done is closed; nil before.
	Err() error
	// Done is closed when the tunnel ends, stopped or failed.
	Done() <-chan struct{}
	// Stop ends the tunnel and what it runs.
	Stop() error
}

// ErrUnavailable is why a provider can't make a tunnel here: its command
// is missing, or it is signed out. Start wraps it with the reason.
var ErrUnavailable = errors.New("tunnel unavailable")

// startTimeout is how long Start waits for the tunnel's URL before
// giving up and ending what it ran. A var, so tests wait less.
var startTimeout = 30 * time.Second

// Start makes the tunnel s asks for, and returns once its URL is known,
// or fails within 30 seconds. ctx bounds the start, not the tunnel's
// life.
func Start(ctx context.Context, s Spec) (Tunnel, error) {
	switch s.Provider {
	case Cloudflare:
		return startCloudflare(ctx, s)
	case Tailscale, Funnel:
		return startTailscale(ctx, s)
	}
	return nil, fmt.Errorf("tunnel: unknown provider %q", s.Provider)
}

// Available says, by provider, why it can't make a tunnel here: "" when
// it can. Each answer runs a command of its own, so ask once and keep
// the answer, not in a loop.
func Available() map[Provider]string {
	ts := whyNotTailscale()
	return map[Provider]string{
		Cloudflare: whyNotCloudflare(),
		Tailscale:  ts,
		Funnel:     ts, // Funnel not turned on is said when it starts
	}
}

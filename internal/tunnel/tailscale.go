package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tailscaleBin is the command that serves on the tailnet.
const tailscaleBin = "tailscale"

// statusTimeout bounds asking Tailscale about itself: it can be slow to
// answer only when it isn't there.
const statusTimeout = 5 * time.Second

// serveTimeout bounds a tailscale serve call, which returns once it has
// set what tailscaled serves.
var serveTimeout = 30 * time.Second

// offTimeout bounds turning a serve off.
const offTimeout = 10 * time.Second

// tailscale is a serve tailscaled keeps running: it has no process of
// its own to watch, so Done waits for Stop.
type tailscale struct {
	port int
	url  string
	done chan struct{}
	once sync.Once
}

// startTailscale serves s's target on this machine's tailnet name, in
// the background, and returns the tunnel.
func startTailscale(ctx context.Context, s Spec) (Tunnel, error) {
	name, err := tailscaleName()
	if err != nil {
		return nil, err
	}
	port := s.Port
	if port == 0 {
		port = 443
	}
	ctx, cancel := context.WithTimeout(ctx, serveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBin, "serve", "--bg", "--https="+strconv.Itoa(port), s.Target)
	hide(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("tailscale serve: %w", ctx.Err())
		}
		if msg := firstLine(string(out)); msg != "" {
			return nil, fmt.Errorf("tailscale serve: %s", msg)
		}
		return nil, fmt.Errorf("tailscale serve: %w", err)
	}
	u := "https://" + name
	if port != 443 {
		u += ":" + strconv.Itoa(port)
	}
	return &tailscale{port: port, url: u, done: make(chan struct{})}, nil
}

func (t *tailscale) URL() string { return t.url }

// Done stays open until Stop: --bg left tailscaled serving, and nothing
// of ours ends by itself.
func (t *tailscale) Done() <-chan struct{} { return t.done }

func (t *tailscale) Err() error { return nil }

// Stop turns the serve off, which ends what tailscaled runs for it.
func (t *tailscale) Stop() error {
	var err error
	t.once.Do(func() {
		defer close(t.done)
		ctx, cancel := context.WithTimeout(context.Background(), offTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, tailscaleBin, "serve", "--https="+strconv.Itoa(t.port), "off")
		hide(cmd)
		out, werr := cmd.CombinedOutput()
		if werr == nil {
			return
		}
		if ctx.Err() == nil {
			if msg := firstLine(string(out)); msg != "" {
				err = fmt.Errorf("tailscale serve off: %s", msg)
				return
			}
		}
		err = fmt.Errorf("tailscale serve off: %w", werr)
	})
	return err
}

// tailscaleName is this machine's name on its tailnet, without the
// trailing dot, when Tailscale is signed in and running.
func tailscaleName() (string, error) {
	if _, err := exec.LookPath(tailscaleBin); err != nil {
		return "", fmt.Errorf("%w: tailscale is not installed", ErrUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBin, "status", "--json")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: Tailscale is signed out", ErrUnavailable)
	}
	var st struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if json.Unmarshal(out, &st) != nil || st.BackendState != "Running" || st.Self.DNSName == "" {
		return "", fmt.Errorf("%w: Tailscale is signed out", ErrUnavailable)
	}
	return strings.TrimSuffix(st.Self.DNSName, "."), nil
}

// whyNotTailscale says what keeps a serve from being made here.
func whyNotTailscale() string {
	if _, err := exec.LookPath(tailscaleBin); err != nil {
		return "tailscale is not installed"
	}
	if _, err := tailscaleName(); err != nil {
		return "Tailscale is signed out"
	}
	return ""
}

// firstLine is the first line that says anything, of a command's output.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

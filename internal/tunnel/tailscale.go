package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
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
const serveTimeout = 30 * time.Second

// offTimeout bounds turning a serve off.
const offTimeout = 10 * time.Second

// serveWatch is how often a running serve is looked up to still be there.
var serveWatch = 30 * time.Second

// tailscale is a serve tailscaled keeps running: it has no process of
// its own to watch, so it is looked up every serveWatch, and Done waits for
// Stop or for the serve to be gone.
type tailscale struct {
	port   int
	target string
	url    string
	done   chan struct{}
	once   sync.Once

	mu  sync.Mutex
	err error
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
	if err := checkServePort(port, s.Target); err != nil {
		return nil, err
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
	trackServe(port, true)
	u := "https://" + name
	if port != 443 {
		u += ":" + strconv.Itoa(port)
	}
	t := &tailscale{port: port, target: s.Target, url: u, done: make(chan struct{})}
	go t.watch()
	return t, nil
}

// watch ends the tunnel, with an error, when tailscaled no longer serves
// its target on its port: the user reset the serve, or signed out. A status
// that can't be read is no proof of that, and is tried again.
func (t *tailscale) watch() {
	tick := time.NewTicker(serveWatch)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tick.C:
		}
		serves, err := TailscaleServe()
		if err != nil {
			continue
		}
		if !slices.Contains(serves[t.port], t.target) {
			t.once.Do(func() {
				t.mu.Lock()
				t.err = fmt.Errorf("tailscale no longer serves %s on port %d", t.target, t.port)
				t.mu.Unlock()
				trackServe(t.port, false)
				close(t.done)
			})
			return
		}
	}
}

func (t *tailscale) URL() string { return t.url }

// Done stays open until Stop or until the serve is gone: --bg left
// tailscaled serving, and no process of ours ends by itself.
func (t *tailscale) Done() <-chan struct{} { return t.done }

// Err is why the serve ended, nil while it runs and after Stop.
func (t *tailscale) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Stop turns the serve off, which ends what tailscaled runs for it.
func (t *tailscale) Stop() error {
	var err error
	t.once.Do(func() {
		defer close(t.done)
		if err = serveOff(t.port); err == nil {
			trackServe(t.port, false)
		}
	})
	return err
}

// serveOff turns off the serve on an https port.
func serveOff(port int) error {
	ctx, cancel := context.WithTimeout(context.Background(), offTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBin, "serve", "--https="+strconv.Itoa(port), "off")
	hide(cmd)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() == nil {
		if msg := firstLine(string(out)); msg != "" {
			return fmt.Errorf("tailscale serve off: %s", msg)
		}
	}
	return fmt.Errorf("tailscale serve off: %w", err)
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

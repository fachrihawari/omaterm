package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// trycloudflare is the URL a Cloudflare quick tunnel gets, in the
// message cloudflared logs when it makes one.
var trycloudflare = regexp.MustCompile(`https://([A-Za-z0-9-]+)\.trycloudflare\.com`)

// quickURL is the tunnel's URL in a line of cloudflared's log, "" when
// there is none. The api subdomain is Cloudflare's own service, which
// cloudflared names when it fails to ask for a tunnel, so it is no tunnel.
func quickURL(line string) string {
	for _, m := range trycloudflare.FindAllStringSubmatch(line, -1) {
		if m[1] != "api" {
			return m[0]
		}
	}
	return ""
}

// logGrace is how long cloudflared's log is read on after it ended: a
// helper it started may hold the log open for good.
const logGrace = 500 * time.Millisecond

// cloudflaredBin is the command that makes a Cloudflare tunnel.
const cloudflaredBin = "cloudflared"

// registered is what cloudflared logs once a named tunnel's connection
// to Cloudflare is up: from then on the hostname reaches it.
const registered = "Registered tunnel connection"

// cloudflare is a running Cloudflare tunnel, quick or named.
type cloudflare struct {
	cmd *exec.Cmd
	url string
	// ready is the tunnel's URL once a line of cloudflared's log says it
	// is up, "" for any other line.
	ready func(line string) string
	// token is the named tunnel's, kept out of the errors said.
	token string
	// done is closed once the command has ended and been waited for.
	done chan struct{}

	mu      sync.Mutex
	stopped bool // Stop or a failed Start ended it
	err     error
	// errLine is the first error cloudflared logged, which says better
	// than its exit status why it ended.
	errLine string
}

// startCloudflare runs a tunnel to s's target and returns it once
// cloudflared says it is up: a quick tunnel when s has no token, at the
// URL cloudflared logs; else the user's named tunnel, at its hostname.
func startCloudflare(ctx context.Context, s Spec) (Tunnel, error) {
	if s.Token != "" && s.Hostname == "" {
		return nil, errors.New("a Cloudflare tunnel token needs the tunnel's public hostname, as term.example.com")
	}
	if _, err := exec.LookPath(cloudflaredBin); err != nil {
		return nil, fmt.Errorf("%w: cloudflared is not installed", ErrUnavailable)
	}
	t := &cloudflare{ready: quickURL, token: s.Token, done: make(chan struct{})}
	var cmd *exec.Cmd
	if s.Token == "" {
		cmd = exec.Command(cloudflaredBin, "tunnel", "--no-autoupdate", "--url", s.Target)
	} else {
		// The token goes in the environment, where cloudflared reads it
		// too: in the arguments, ps would show it to anyone.
		cmd = exec.Command(cloudflaredBin, "tunnel", "--no-autoupdate", "run")
		cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+s.Token)
		url := "https://" + s.Hostname
		t.ready = func(line string) string {
			if strings.Contains(line, registered) {
				return url
			}
			return ""
		}
	}
	t.cmd = cmd
	hide(cmd)
	inGroup(cmd) // stopped as a group: cloudflared runs helpers of its own
	// The log goes to a pipe of our own, so that waiting for the command
	// does not wait for whoever else holds the pipe's write end.
	stderr, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = w
	err = cmd.Start()
	w.Close()
	if err != nil {
		stderr.Close()
		return nil, fmt.Errorf("cloudflared: %w", err)
	}
	found := make(chan string, 1)
	go t.watch(stderr, found)
	select {
	case u := <-found:
		t.url = u
		return t, nil
	case <-t.done: // it ended before saying a URL (it may just have said one)
		select {
		case u := <-found:
			t.url = u
			return t, nil
		default:
		}
		if err := t.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("cloudflared ended before its URL")
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	case <-time.After(startTimeout):
		t.Stop()
		if s.Token != "" {
			return nil, fmt.Errorf("cloudflared did not connect the tunnel in %s", startTimeout)
		}
		return nil, fmt.Errorf("cloudflared gave no URL in %s", startTimeout)
	}
}

// watch reads cloudflared's log for the tunnel being up, and after its
// end closes done and keeps why it ended, if it wasn't stopped. The log is
// read on for logGrace after the command ended, and then dropped.
func (t *cloudflare) watch(r *os.File, found chan<- string) {
	read := make(chan struct{})
	go func() {
		defer close(read)
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if u := t.ready(line); u != "" {
				select {
				case found <- u:
				default:
				}
			}
			if msg := errLine(line); msg != "" {
				t.mu.Lock()
				if t.errLine == "" {
					t.errLine = msg
				}
				t.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	werr := t.cmd.Wait()
	select {
	case <-read:
	case <-time.After(logGrace):
	}
	r.Close()
	t.mu.Lock()
	if !t.stopped && werr != nil {
		why := werr.Error()
		if t.errLine != "" {
			why = t.errLine
		}
		if t.token != "" {
			why = strings.ReplaceAll(why, t.token, "…")
		}
		t.err = fmt.Errorf("cloudflared: %s", why)
	}
	t.mu.Unlock()
	close(t.done)
}

// errLine is what an error line of cloudflared's log says, past its time
// and level: "" for a line of another level.
func errLine(line string) string {
	_, msg, ok := strings.Cut(line, "ERR ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(msg)
}

func (t *cloudflare) URL() string { return t.url }

func (t *cloudflare) Done() <-chan struct{} { return t.done }

// Err is why the tunnel ended: nil while it runs, and after it is
// stopped, which is no failure.
func (t *cloudflare) Err() error {
	select {
	case <-t.done:
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.err
	default:
		return nil
	}
}

// Stop kills cloudflared and its helpers, and waits for them.
func (t *cloudflare) Stop() error {
	t.mu.Lock()
	already := t.stopped
	t.stopped = true
	t.mu.Unlock()
	if already {
		<-t.done
		return nil
	}
	// A command that ended is waited for: its group id may be another
	// process's by now.
	select {
	case <-t.done:
		return nil
	default:
	}
	killGroup(t.cmd)
	<-t.done
	return nil
}

// whyNotCloudflare says what keeps a Cloudflare tunnel from being made
// here.
func whyNotCloudflare() string {
	if _, err := exec.LookPath(cloudflaredBin); err != nil {
		return "cloudflared is not installed"
	}
	return ""
}

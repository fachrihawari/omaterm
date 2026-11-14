// Package sshconn runs omaterm's bridge on another computer over SSH and
// gives its standard input and output as a net.Conn.
package sshconn

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Dial runs `omaterm __bridge` on target, a host or user@host as ssh takes
// it, and returns the bridge's connection. The bridge starts the session
// daemon on the host when none runs.
func Dial(target string) (net.Conn, error) { return DialVersion(target, "") }

// DialVersion is Dial, first refusing a host whose omaterm is another
// version: an older one would open its window there instead of the bridge.
func DialVersion(target, version string) (net.Conn, error) {
	if target == "" {
		return nil, errors.New("no SSH host")
	}
	if strings.HasPrefix(target, "-") {
		return nil, fmt.Errorf("bad SSH host %q", target)
	}
	cmd := exec.Command("ssh", append(sshArgs(), target, remoteCommand(version))...)
	cmd.Env = os.Environ()
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return nil, err
	}
	// The child holds its ends now: the parent's copies close, so the
	// bridge sees EOF when the app's side closes.
	inR.Close()
	outW.Close()
	errW.Close()

	c := &conn{target: target, cmd: cmd, in: inW, out: outR,
		done: make(chan struct{}), errDone: make(chan struct{})}
	go func() {
		var b bytes.Buffer
		io.Copy(&limitedWriter{b: &b, left: 8 << 10}, errR)
		errR.Close()
		c.mu.Lock()
		c.stderr = strings.TrimSpace(b.String())
		c.mu.Unlock()
		close(c.errDone)
	}()
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.waitErr = err
		c.mu.Unlock()
		close(c.done)
	}()
	return c, nil
}

// Run runs command on target over SSH, under sh, and returns its standard
// output. The app's setup steps use it before the bridge: a probe, an
// install.
func Run(target, command string) (string, error) { return Stream(target, command, nil) }

// Stream is Run with r as the command's standard input.
func Stream(target, command string, r io.Reader) (string, error) {
	if target == "" {
		return "", errors.New("no SSH host")
	}
	if strings.HasPrefix(target, "-") {
		return "", fmt.Errorf("bad SSH host %q", target)
	}
	cmd := exec.Command("ssh", append(sshArgs(), target, "sh -c "+quote(command))...)
	cmd.Env = os.Environ()
	cmd.Stdin = r
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), &RemoteError{Message: msg}
	}
	return out.String(), nil
}

// sshArgs are the options every bridge gets: no terminal, no prompts (a
// key or an agent answers), and no forwarding the user's SSH config might
// add.
func sshArgs() []string {
	return []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=10",
		"-o", "ServerAliveCountMax=3",
		"-o", "ClearAllForwardings=yes",
	}
}

// remoteCommand finds omaterm on the host, as a login shell's PATH may
// not hold it, and runs the bridge. It is run by the host's login shell,
// whatever that is, so it starts sh: fish and csh take that too. A
// version given is checked first, so a binary that predates the bridge
// never opens its window there.
func remoteCommand(version string) string {
	script := `p="$HOME/.local/bin/omaterm"; `
	if os.Getenv("OMATERM_DEV") == "1" {
		// A dev build keeps its files apart, on the host too.
		script = `export OMATERM_DEV=1; p="$HOME/.local/state/omaterm-dev/bin/omaterm"; `
	}
	script += `[ -x "$p" ] || p="$HOME/.local/bin/omaterm"; ` +
		`[ -x "$p" ] || p="$HOME/.config/omaterm/bin/omaterm"; ` +
		`[ -x "$p" ] || p=$(command -v omaterm 2>/dev/null); ` +
		`if [ -z "$p" ]; then echo "omaterm is not installed on this host" >&2; exit 127; fi; `
	if versionWord(version) {
		script += `v=$("$p" version 2>/dev/null); v=${v##* }; ` +
			`if [ "$v" != "` + version + `" ]; then echo "omaterm ${v:-on this host} is not ` + version + `" >&2; exit 1; fi; `
	}
	script += `exec "$p" __bridge`
	return "sh -c " + quote(script)
}

// versionWord tells a version that can go into the shell script: letters,
// digits and the punctuation a build uses, nothing a shell would read.
func versionWord(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) {
			return false
		}
	}
	return true
}

// quote makes s one word for a POSIX shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// conn is the bridge's standard input and output as a net.Conn.
type conn struct {
	target string
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *os.File
	done   chan struct{}
	// errDone is closed when the stderr reader is done, so an error can
	// say why ssh or the bridge stopped.
	errDone chan struct{}

	mu       sync.Mutex
	stderr   string
	waitErr  error
	timer    *time.Timer
	timedOut bool
	pending  error
	closed   bool
}

func (c *conn) Read(b []byte) (int, error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	if pending != nil {
		return 0, pending
	}
	n, err := c.out.Read(b)
	if err == nil {
		return n, nil
	}
	<-c.done
	<-c.errDone
	c.mu.Lock()
	timedOut, stderr, waitErr := c.timedOut, c.stderr, c.waitErr
	c.mu.Unlock()
	var e error
	switch {
	case timedOut:
		e = os.ErrDeadlineExceeded
	case stderr != "":
		e = &RemoteError{Message: stderr}
	case waitErr != nil:
		e = waitErr
	default:
		e = io.EOF
	}
	if n > 0 {
		c.mu.Lock()
		c.pending = e
		c.mu.Unlock()
		return n, nil
	}
	return 0, e
}

func (c *conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return c.in.Write(b)
}

func (c *conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	c.in.Close()
	c.kill()
	<-c.done
	c.out.Close()
	return nil
}

func (c *conn) kill() {
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}

func (c *conn) LocalAddr() net.Addr  { return sshAddr{"local"} }
func (c *conn) RemoteAddr() net.Addr { return sshAddr{c.target} }

func (c *conn) SetDeadline(t time.Time) error {
	c.SetWriteDeadline(t)
	return c.SetReadDeadline(t)
}

// SetReadDeadline kills the bridge when the deadline passes: a read on a
// pipe cannot time out by itself.
func (c *conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.timedOut = false
	if t.IsZero() {
		return nil
	}
	d := time.Until(t)
	if d <= 0 {
		c.timedOut = true
		go c.kill()
		return nil
	}
	c.timer = time.AfterFunc(d, func() {
		c.mu.Lock()
		c.timedOut = true
		c.mu.Unlock()
		c.kill()
	})
	return nil
}

func (c *conn) SetWriteDeadline(time.Time) error { return nil }

type sshAddr struct{ s string }

func (a sshAddr) Network() string { return "ssh" }
func (a sshAddr) String() string  { return a.s }

// RemoteError is what the host said before the connection closed: ssh's
// permission denied, or the bridge's missing omaterm.
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return e.Message }

// limitedWriter keeps the end of what it is given: ssh's last lines say
// what went wrong, and a login banner may come first.
type limitedWriter struct {
	b    *bytes.Buffer
	left int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if len(p) >= w.left {
		w.b.Reset()
		w.b.Write(p[len(p)-w.left:])
		return len(p), nil
	}
	w.b.Write(p)
	w.left -= len(p)
	return len(p), nil
}

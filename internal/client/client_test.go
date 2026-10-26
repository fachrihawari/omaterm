package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/ctl"
	"omaterm/internal/proto"
)

type nopHost struct{}

func (nopHost) Handle(context.Context, string, json.RawMessage) (any, error) { return true, nil }
func (nopHost) Subscribe(func(proto.Message)) func()                         { return func() {} }

// A server of another protocol refuses the client, telling its process,
// so the app can offer to restart it.
func TestRefusedTellsPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	srv, err := ctl.Listen(path, nopHost{}, "old")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	helloVersion = proto.Version + 1
	defer func() { helloVersion = proto.Version }()
	_, err = Dial(path)
	var ref *RefusedError
	if !errors.As(err, &ref) || ref.Welcome.PID != os.Getpid() || ref.Welcome.App != "old" {
		t.Fatalf("err %v, want a refusal from pid %d", err, os.Getpid())
	}
}

// A socket this user may not open says so; it was "not running".
func TestDialSaysWhy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	_, err = Dial(path)
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("got %v, want a permission error", err)
	}
}

type slowHost struct{ nopHost }

func (slowHost) Handle(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A call given up on leaves no trace: each one's channel stayed, one per
// timeout, for as long as the connection.
func TestTimedOutCallForgotten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	srv, err := ctl.Listen(path, slowHost{}, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		c.CallContext(ctx, "slow", nil, nil)
		cancel()
	}
	c.mu.Lock()
	n := len(c.calls)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("%d calls given up on are still kept", n)
	}
}

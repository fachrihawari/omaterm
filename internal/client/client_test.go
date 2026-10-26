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

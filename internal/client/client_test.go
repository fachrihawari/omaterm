package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

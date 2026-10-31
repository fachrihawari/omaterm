package ctl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

type fakeHost struct {
	mu   sync.Mutex
	subs []func(proto.Message)
}

func (h *fakeHost) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "echo":
		var v map[string]any
		json.Unmarshal(raw, &v)
		return v, nil
	case "slow":
		time.Sleep(200 * time.Millisecond)
		return "slow", nil
	case "fail":
		return nil, proto.Errorf(proto.CodeNotFound, "nope")
	case "panic":
		var m map[string]int
		m["boom"] = 1 // a bug: a nil map
	}
	return nil, proto.Errorf(proto.CodeUsage, "no method "+method)
}

func (h *fakeHost) Subscribe(fn func(proto.Message)) func() {
	h.mu.Lock()
	h.subs = append(h.subs, fn)
	h.mu.Unlock()
	return func() {}
}

func serve(t *testing.T) (*Server, *fakeHost) {
	t.Helper()
	h := &fakeHost{}
	path := filepath.Join(t.TempDir(), "s.sock")
	s, err := Listen(path, h, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, h
}

func TestCalls(t *testing.T) {
	s, _ := serve(t)
	// Windows keeps no Unix modes to check.
	if st, _ := os.Stat(s.Path()); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", st.Mode().Perm())
	}
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Welcome.Version != proto.Version || c.Welcome.App != "test" {
		t.Errorf("welcome %+v", c.Welcome)
	}
	var out map[string]any
	if err := c.Call("echo", map[string]any{"a": 1.0}, &out); err != nil || out["a"] != 1.0 {
		t.Fatalf("echo %v %v", out, err)
	}
	err = c.Call("fail", nil, nil)
	if proto.CodeOf(err) != proto.CodeNotFound {
		t.Fatalf("fail: %v", err)
	}
	if proto.CodeOf(c.Call("nothing", nil, nil)) != proto.CodeUsage {
		t.Fatal("unknown method")
	}
	// Calls run side by side: a fast one is not held by a slow one.
	done := make(chan string, 2)
	go func() { var s string; c.Call("slow", nil, &s); done <- s }()
	time.Sleep(20 * time.Millisecond)
	go func() { var v map[string]any; c.Call("echo", map[string]any{}, &v); done <- "echo" }()
	if first := <-done; first != "echo" {
		t.Fatalf("first answer %q", first)
	}
	<-done
}

func TestSecondServerRefused(t *testing.T) {
	s, _ := serve(t)
	if _, err := Listen(s.Path(), &fakeHost{}, "x"); err != ErrRunning {
		t.Fatalf("second Listen: %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	if _, err := client.Dial(filepath.Join(t.TempDir(), "none.sock")); err != client.ErrUnreachable {
		t.Fatalf("%v", err)
	}
}

func TestSubscribe(t *testing.T) {
	s, h := serve(t)
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call("subscribe", nil, nil); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	fn := h.subs[0]
	h.mu.Unlock()
	fn(proto.Message{Event: "state", State: &proto.State{Viewed: "s1"}})
	select {
	case m := <-c.Events():
		if m.State == nil || m.State.Viewed != "s1" {
			t.Fatalf("%+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
}

// A call that panics is answered with an error, and the server, which may
// be the session daemon running every session, goes on.
func TestPanicInCall(t *testing.T) {
	s, _ := serve(t)
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call("panic", nil, nil); proto.CodeOf(err) != proto.CodeInternal {
		t.Fatalf("panic: %v, want an internal error", err)
	}
	var out map[string]any
	if err := c.Call("echo", map[string]any{"a": 1.0}, &out); err != nil || out["a"] != 1.0 {
		t.Fatalf("echo after the panic: %v %v", out, err)
	}
}

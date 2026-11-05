//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/session"
)

// The page seeing a pane clears its session's unread marks, and only by a
// POST from the page itself.
func TestWebSeen(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	params, _ := json.Marshal(daemon.StartParams{ID: "a1", Session: "s1", Exec: true, Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", `printf '\033]7501;state=error\007'; exec cat`}})
	if _, err := f.d.Handle(ctx, "pane.start", params); err != nil {
		t.Fatal(err)
	}
	unread := func() bool {
		v, _ := f.d.Handle(ctx, "panes", nil)
		i, ok := v.(map[string]session.Info)["a1"]
		if !ok {
			t.Fatal("no pane a1")
		}
		return i.Unread
	}
	for deadline := time.Now().Add(5 * time.Second); !unread(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the pane never got unread")
		}
	}

	c := f.paired(t)
	if res := f.post(t, f.client(t), "/api/panes/a1/seen", "", f.base); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired: %s", res.Status)
	}
	for _, origin := range []string{"", "https://evil.example"} {
		if res := f.post(t, c, "/api/panes/a1/seen", "", origin); res.StatusCode != http.StatusForbidden {
			t.Fatalf("from origin %q: %s", origin, res.Status)
		}
	}
	if res, _ := c.Get(f.base + "/api/panes/a1/seen"); res.StatusCode == 200 {
		t.Fatalf("a GET: %s", res.Status)
	}
	if !unread() {
		t.Fatal("seen by a refused request")
	}
	if res := f.post(t, c, "/api/panes/nope/seen", "", f.base); res.StatusCode != http.StatusNotFound {
		t.Fatalf("no such pane: %s", res.Status)
	}
	if res := f.post(t, c, "/api/panes/a1/seen", "", f.base); res.StatusCode != 200 {
		t.Fatalf("seen: %s", res.Status)
	}
	if unread() {
		t.Fatal("still unread once seen")
	}
}

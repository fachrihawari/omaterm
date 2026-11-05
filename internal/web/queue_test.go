//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/proto"
)

// session starts a session in the fixture's folder running command, and
// returns its pane once the state shows it as ok has it.
func (f *fixture) session(t *testing.T, name string, command []string, ok func(proto.Pane) bool) string {
	t.Helper()
	ctx := context.Background()
	params, _ := json.Marshal(proto.CreateParams{Dir: f.dir, Name: name, Command: command})
	v, err := f.d.Handle(ctx, "create", params)
	if err != nil {
		t.Fatal(err)
	}
	pane := v.(proto.Created).Pane
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		st, _ := f.d.Handle(ctx, "state", nil)
		for _, s := range st.(proto.State).Sessions {
			for _, p := range s.Panes {
				if p.ID == pane && ok(p) {
					return pane
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the state never showed %s as wanted: %+v", name, st)
		}
	}
}

// busyAgent starts a fake agent, a script named claude as omaterm knows
// the agent by its process, which works on and on: what is queued for it
// waits.
func (f *fixture) busyAgent(t *testing.T) string {
	t.Helper()
	script := filepath.Join(f.dir, "bin", "claude")
	os.MkdirAll(filepath.Dir(script), 0o755)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '\\033]7501;state=working\\007working…\\n'\nwhile read -r l; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f.session(t, "agent", []string{"/bin/sh", "-c", "set -m; '" + script + "'; :"}, func(p proto.Pane) bool {
		return p.Agent == "claude" && p.Status == "working"
	})
}

func (f *fixture) queued(t *testing.T, c *http.Client, pane string) []struct{ ID, Text string } {
	t.Helper()
	res := f.do(t, c, "GET", "/api/panes/"+pane+"/queue", "")
	defer res.Body.Close()
	var list []struct{ ID, Text string }
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&list) != nil {
		t.Fatalf("queue: %s", res.Status)
	}
	return list
}

// The page lines up prompts for an agent, typed in once it is free: notes
// from omaterm itself, whatever the page says, at most a few a pane, and
// only for an agent. It lists and takes back its own, and no others.
func TestWebQueue(t *testing.T) {
	f := newFixture(t)
	c := f.paired(t)
	agent := f.busyAgent(t)
	shell := f.session(t, "shell", []string{"sleep", "60"}, func(proto.Pane) bool { return true })
	ctx := context.Background()

	res := f.do(t, c, "POST", "/api/panes/"+agent+"/queue", `{"text":"run the tests","from":"`+shell+`","fromName":"lead","note":false}`)
	var q struct{ ID, Text string }
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&q) != nil || q.Text != "run the tests" {
		t.Fatalf("queue: %s %+v", res.Status, q)
	}
	params, _ := json.Marshal(proto.MailListParams{Pane: agent})
	v, _ := f.d.Handle(ctx, "mail.list", params)
	if list := v.([]proto.Mail); len(list) != 1 || list[0].From != "" || list[0].FromName != "web access" || !list[0].Note ||
		list[0].State != proto.MailQueued || list[0].ToName != "agent" {
		t.Fatalf("the mailbox has %+v", list)
	}
	if list := f.queued(t, c, agent); len(list) != 1 || list[0].ID != q.ID {
		t.Fatalf("the page lists %+v", list)
	}

	for _, bad := range []struct {
		pane, body string
		status     int
	}{
		{shell, `{"text":"hi"}`, http.StatusConflict},
		{"nope", `{"text":"hi"}`, http.StatusNotFound},
		{agent, `{"text":"  "}`, http.StatusBadRequest},
		{agent, `{"text":"` + strings.Repeat("x", 64<<10+1) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		if res := f.do(t, c, "POST", "/api/panes/"+bad.pane+"/queue", bad.body); res.StatusCode != bad.status {
			t.Errorf("queue %.20s for %s: %s, want %d", bad.body, bad.pane, res.Status, bad.status)
		}
	}
	for i := range 4 {
		if res := f.do(t, c, "POST", "/api/panes/"+agent+"/queue", `{"text":"more"}`); res.StatusCode != 200 {
			t.Fatalf("prompt %d: %s", i+2, res.Status)
		}
	}
	if res := f.do(t, c, "POST", "/api/panes/"+agent+"/queue", `{"text":"one too many"}`); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a sixth prompt: %s", res.Status)
	}

	// Another site's page can't queue or cancel, nor an unpaired browser
	// list.
	for _, m := range []string{"POST", "DELETE"} {
		req, _ := http.NewRequest(m, f.base+"/api/panes/"+agent+"/queue/"+q.ID, strings.NewReader(`{"text":"x"}`))
		if m == "POST" {
			req.URL.Path = "/api/panes/" + agent + "/queue"
		}
		req.Header.Set("Origin", "https://evil.example")
		if res, _ := c.Do(req); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s from another site: %s", m, res.Status)
		}
	}
	if res, _ := f.client(t).Get(f.base + "/api/panes/" + agent + "/queue"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unpaired list: %s", res.Status)
	}

	// A note of omaterm's own for the pane is neither listed nor taken back.
	params, _ = json.Marshal(proto.MailAskParams{To: agent, Text: "a review's brief", Note: true})
	v, err := f.d.Handle(ctx, "mail.ask", params)
	if err != nil {
		t.Fatal(err)
	}
	other := v.(proto.Mail).ID
	if list := f.queued(t, c, agent); len(list) != 5 {
		t.Fatalf("the page lists %d", len(list))
	}
	if res := f.do(t, c, "DELETE", "/api/panes/"+agent+"/queue/"+other, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("cancelled omaterm's own note: %s", res.Status)
	}

	if res := f.do(t, c, "DELETE", "/api/panes/"+agent+"/queue/"+q.ID, ""); res.StatusCode != 200 {
		t.Fatalf("cancel: %s", res.Status)
	}
	for _, m := range f.queued(t, c, agent) {
		if m.ID == q.ID {
			t.Fatalf("still listed after cancel")
		}
	}
	if res := f.do(t, c, "DELETE", "/api/panes/"+agent+"/queue/"+q.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("cancelled twice: %s", res.Status)
	}
	if res := f.do(t, c, "POST", "/api/panes/"+agent+"/queue", `{"text":"room again"}`); res.StatusCode != 200 {
		t.Fatalf("after a cancel: %s", res.Status)
	}
}

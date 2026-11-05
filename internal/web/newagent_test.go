//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/web"
)

// newAgentFixture is filesFixture with a light shell for new sessions,
// and an agent on the PATH that only reads: no real one starts.
func newAgentFixture(t *testing.T) (*fixture, *http.Client) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filesFixture(t)
}

func (f *fixture) state(t *testing.T) proto.State {
	t.Helper()
	v, err := f.d.Handle(context.Background(), "state", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var st proto.State
	json.Unmarshal(raw, &st)
	return st
}

func readAll(res *http.Response) string {
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return strings.TrimSpace(string(b))
}

// The page names an agent of the list, never a command: anything else,
// a bad branch, or a task for a shell, is refused before a session is
// made.
func TestCreateRefuses(t *testing.T) {
	f, c := newAgentFixture(t)
	before := len(f.state(t).Sessions)
	for _, body := range []string{
		`{"project":"p1","command":["sh","-c","touch /tmp/x"]}`,
		`{"project":"p1","agent":"sh"}`,
		`{"project":"p1","agent":"claude; touch /tmp/x"}`,
		`{"project":"p1","agent":"shell","task":"fix it"}`,
		`{"project":"p1","task":"fix it"}`,
		`{"project":"p1","agent":"claude","task":"` + strings.Repeat("x", 17<<10) + `"}`,
		`{"project":"p1","worktree":{"branch":"-b"}}`,
		`{"project":"p1","worktree":{"branch":"--upload-pack=x"}}`,
		`{"project":"p1","worktree":{"branch":"a..b"}}`,
		`{"project":"p1","worktree":{"branch":""}}`,
		`{"project":"p1","worktree":{"branch":"` + strings.Repeat("b", 101) + `"}}`,
		`{"project":"p1","worktree":{"branch":"x","base":"main"}}`,
		`{"agent":"claude"}`,
	} {
		res := f.post(t, c, "/api/sessions", body, f.base)
		if msg := readAll(res); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%.80s: %s %s", body, res.Status, msg)
		}
	}
	if n := len(f.state(t).Sessions); n != before {
		t.Fatalf("%d sessions, was %d", n, before)
	}
	// Another site's page can't start one.
	if res := f.post(t, c, "/api/sessions", `{"project":"p1"}`, "https://evil.example"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: %s", res.Status)
	}
	if res := f.post(t, f.client(t), "/api/sessions", `{"project":"p1"}`, f.base); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired: %s", res.Status)
	}
}

// A shell session, as '+' made before the sheet, from {project} alone.
func TestCreateShell(t *testing.T) {
	f, c := newAgentFixture(t)
	res := f.post(t, c, "/api/sessions", `{"project":"p1"}`, f.base)
	var made proto.Created
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil || res.StatusCode != 200 || made.Pane == "" {
		t.Fatalf("%s %v %+v", res.Status, err, made)
	}
	for _, s := range f.state(t).Sessions {
		if s.ID == made.Session {
			return
		}
	}
	t.Fatalf("no session %s", made.Session)
}

// An agent's first task waits in the mailbox as a note of omaterm's,
// typed in once the agent is at its prompt.
func TestCreateAgentWithTask(t *testing.T) {
	f, c := newAgentFixture(t)
	res := f.post(t, c, "/api/sessions", `{"project":"p1","agent":"claude","task":"Fix the login page"}`, f.base)
	var made map[string]string
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil || res.StatusCode != 200 || made["pane"] == "" || made["taskError"] != "" {
		t.Fatalf("%s %v %v", res.Status, err, made)
	}
	params, _ := json.Marshal(proto.MailListParams{Pane: made["pane"]})
	v, err := f.d.Handle(context.Background(), "mail.list", params)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	var list []proto.Mail
	json.Unmarshal(raw, &list)
	if len(list) != 1 || list[0].Text != "Fix the login page" || !list[0].Note || list[0].From != "" || list[0].To != made["pane"] {
		t.Fatalf("mail %+v", list)
	}
}

// Own worktree: a new branch from the project's, in its .worktrees; and,
// slower than the page waits, answered 202 with the branch, the session
// made all the same.
func TestCreateWorktree(t *testing.T) {
	f, c := newAgentFixture(t)
	res := f.post(t, c, "/api/sessions", `{"project":"p1","agent":"shell","worktree":{"branch":"phone/fix"}}`, f.base)
	var made proto.Created
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil || res.StatusCode != 200 {
		t.Fatalf("%s %v", res.Status, err)
	}
	var w *model.Worktree
	for _, s := range f.state(t).Sessions {
		if s.ID == made.Session {
			w = s.Worktree
		}
	}
	if w == nil || w.Branch != "phone/fix" || w.Base != "main" || !strings.Contains(w.Dir, ".worktrees") {
		t.Fatalf("worktree %+v", w)
	}
	if _, err := os.Stat(w.Dir); err != nil {
		t.Fatal(err)
	}

	defer web.SetCreateWait(0)()
	res = f.post(t, c, "/api/sessions", `{"project":"p1","worktree":{"branch":"slow"}}`, f.base)
	if msg := readAll(res); res.StatusCode != http.StatusAccepted || !strings.Contains(msg, `"branch":"slow"`) {
		t.Fatalf("slow: %s %s", res.Status, msg)
	}
	for i := 0; ; i++ {
		found := false
		for _, s := range f.state(t).Sessions {
			found = found || s.Worktree != nil && s.Worktree.Branch == "slow"
		}
		if found {
			break
		}
		if i > 100 {
			t.Fatal("the slow worktree's session never came")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The sheet's choices: the agents, a shell always there, and the
// project's branch for its worktree.
func TestNewOptions(t *testing.T) {
	f, c := newAgentFixture(t)
	var got struct {
		Agents []struct {
			Name, Label string
			Found       bool
		}
		Git    bool
		Branch string
	}
	if code := getJSON(t, c, f.base+"/api/new?project=p1", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	names := map[string]bool{}
	for _, a := range got.Agents {
		names[a.Name] = a.Found
	}
	if len(got.Agents) != 6 || !names["shell"] || !names["claude"] || got.Agents[0].Label != "Claude Code" {
		t.Fatalf("agents %+v", got.Agents)
	}
	if !got.Git || got.Branch != "main" {
		t.Fatalf("git %v, branch %q", got.Git, got.Branch)
	}
	var none struct{ Git bool }
	if code := getJSON(t, c, f.base+"/api/new?project=nope", &none); code != 200 || none.Git {
		t.Fatalf("no project: %d %v", code, none.Git)
	}
	if res, _ := f.client(t).Get(f.base + "/api/new"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired: %s", res.Status)
	}
}

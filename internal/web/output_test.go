//go:build linux || darwin

package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/proto"
	"omaterm/internal/web"
)

// paneText is what the text route answers.
type paneText struct {
	Text    string `json:"text"`
	More    bool   `json:"more"`
	Stopped string `json:"stopped"`
}

// waitText reads the pane's text until it holds want.
func waitText(t *testing.T, c *http.Client, f *fixture, pane, want string) paneText {
	t.Helper()
	var got paneText
	for i := 0; i < 100; i++ {
		got = paneText{}
		if code := getJSON(t, c, f.base+"/api/panes/"+pane+"/text", &got); code != http.StatusOK {
			t.Fatalf("text of %s: %d", pane, code)
		}
		if strings.Contains(got.Text, want) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("text of %s has no %q: %q", pane, want, got.Text)
	return got
}

// A pane's text is its scrollback and screen, a long line in one piece
// though the terminal wrapped it, for a paired browser only; the last
// megabyte of it at most, cut at a line.
func TestWebPaneText(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("0123456789", 12)
	params, _ := json.Marshal(daemon.StartParams{ID: "t1", Exec: true, Cols: 40, Rows: 10,
		Command: []string{"sh", "-c", "for i in $(seq 1 30); do echo line $i; done; echo " + long + "; exec sleep 600"}})
	if _, err := f.d.Handle(context.Background(), "pane.start", params); err != nil {
		t.Fatal(err)
	}

	unpaired := f.client(t)
	if res, err := unpaired.Get(f.base + "/api/panes/t1/text"); err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired: %v %v", res.Status, err)
	}

	c := f.paired(t)
	got := waitText(t, c, f, "t1", long)
	if !strings.Contains(got.Text, "line 1\n") || !strings.Contains(got.Text, "\n"+long+"\n") || got.More || got.Stopped != "" {
		t.Fatalf("text %+v", got)
	}
	if code := getJSON(t, c, f.base+"/api/panes/nope/text", nil); code != http.StatusNotFound {
		t.Fatalf("no such pane: %d", code)
	}

	defer web.SetMaxText(200)()
	got = paneText{}
	getJSON(t, c, f.base+"/api/panes/t1/text", &got)
	if !got.More || len(got.Text) > 200 || !strings.HasPrefix(got.Text, "line ") || !strings.HasSuffix(got.Text, "\n"+long+"\n") {
		t.Fatalf("capped text %+v", got)
	}
}

// A process's run that ended took its pane with it, but the page still
// reads its last lines, which the daemon kept, and omaterm read too;
// reading them starts nothing.
func TestWebStoppedProcessText(t *testing.T) {
	f := newProcessFixture(t, "echo building; echo listen EADDRINUSE: address already in use; exit 3")
	c := f.paired(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	params, _ := json.Marshal(proto.ProcessParams{Project: "p1", Name: "web"})
	if _, err := f.d.Handle(ctx, "process.start", params); err != nil {
		t.Fatal(err)
	}
	var failed proto.Session
	for {
		failed = processSession(t, c, f, "web")
		if failed.Proc != nil && failed.Proc.Status == "failed" {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("the process did not fail: %+v", failed.Proc)
		}
		time.Sleep(50 * time.Millisecond)
	}
	pane := failed.Panes[0].ID
	got := waitText(t, c, f, pane, "EADDRINUSE")
	if got.Stopped != "web" || !strings.Contains(got.Text, "building\n") {
		t.Fatalf("stopped process's text %+v", got)
	}
	for _, lines := range []int{0, 1} {
		read, _ := json.Marshal(proto.ReadParams{ID: pane, Lines: lines})
		v, err := f.d.Handle(ctx, "read", read)
		if err != nil {
			t.Fatalf("omaterm read of the stopped process: %v", err)
		}
		text := v.(proto.ReadResult).Text
		if lines == 0 && text != got.Text || lines == 1 && (strings.Count(text, "\n") != 1 || strings.Contains(text, "building")) {
			t.Fatalf("omaterm read -n %d of the stopped process: %q", lines, text)
		}
	}
	if s := processSession(t, c, f, "web"); s.Proc.Status != "failed" || s.Panes[0].Status != "exited" {
		t.Fatalf("reading started the process: %+v", s.Proc)
	}
}

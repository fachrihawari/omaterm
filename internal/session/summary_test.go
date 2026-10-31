//go:build linux || darwin

package session

import (
	"strings"
	"testing"
	"time"

	"omaterm/internal/osc"
)

// An agent's last word on its turn comes from its screen, once it is at
// its prompt, or from a report that says something; a turn starting
// takes it away.
func TestSummary(t *testing.T) {
	prev := procName
	procName = func(pid int) string { return "claude" }
	defer func() { procName = prev }()
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `set -m; printf '\033]7501;state=working\007'; sleep 0.3; `+
		`printf '\n⏺ All tests pass now, and the\n  lint is clean.\n\n'; printf '\033]7501;state=done\007────────────────────\n❯ \n────────────────────\n'; sleep 5; :`)
	time.Sleep(200 * time.Millisecond)
	r.Tick(time.Now())
	waitFor(t, p, "working", func(i Info) bool { return i.Status == Working })
	waitFor(t, p, "done", func(i Info) bool { return i.Status == Idle && len(i.Records) > 0 })
	time.Sleep(100 * time.Millisecond)
	r.Tick(time.Now()) // the screen read: its prompt shows, with its last word above
	if got := p.Info().Summary; got != "All tests pass now, and the lint is clean." {
		t.Fatalf("summary from the screen %q", got)
	}
	report := func(rep osc.Record) {
		t.Helper()
		body := strings.TrimSuffix(strings.TrimPrefix(osc.Encode(rep), "\x1b]7501;"), "\x1b\\")
		if err := p.Report(body); err != nil {
			t.Fatal(err)
		}
	}
	report(osc.Record{ID: "agent", State: "working"})
	if got := p.Info().Summary; got != "" {
		t.Fatalf("summary through a turn %q", got)
	}
	report(osc.Record{ID: "agent", State: "done", Msg: "Fixed the login redirect"})
	if got := p.Info().Summary; got != "Fixed the login redirect" {
		t.Fatalf("summary from a report %q", got)
	}
}

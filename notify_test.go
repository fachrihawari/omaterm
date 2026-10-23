package main

import (
	"testing"

	"omaterm/internal/session"
)

func TestNoticeFor(t *testing.T) {
	all := NotifySettings{Waiting: true, Done: true, Error: true}
	p := func(st session.Status) session.Info { return session.Info{ID: "p", Status: st} }
	cases := []struct {
		name       string
		prev, next session.Info
		here       bool
		prefs      NotifySettings
		want       noticeKind
	}{
		{"waiting", p(session.Working), p(session.Waiting), false, all, noticeWaiting},
		{"here", p(session.Working), p(session.Waiting), true, all, noticeNone},
		{"off", p(session.Working), p(session.Waiting), false, NotifySettings{}, noticeNone},
		{"failed", p(session.Working), p(session.Failed), false, all, noticeFailed},
		{"done unread", p(session.Working), session.Info{ID: "p", Status: session.Idle, Unread: true}, false, all, noticeDone},
		{"done seen", p(session.Working), p(session.Idle), false, all, noticeNone},
		{"still waiting", p(session.Waiting), p(session.Waiting), false, all, noticeNone},
		{"new pane", session.Info{}, p(session.Waiting), false, all, noticeNone},
	}
	for _, c := range cases {
		if got := noticeFor(c.prev, c.next, c.here, c.prefs); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// Ctrl+Shift+J goes to the session waiting for the user, across projects.
func TestJumpToAttention(t *testing.T) {
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	a.switchProject(1)
	bTab := a.tabs[0]
	a.status[bTab.Split.termIDs()[0]] = session.Info{ID: bTab.Split.termIDs()[0], Session: bTab.ID, Status: session.Waiting}
	a.switchProject(0)
	a.jumpToAttention(nil)
	if a.project().ID != "pb" || a.tabs[a.activeTab].ID != bTab.ID {
		t.Fatalf("on %s/%d", a.project().ID, a.activeTab)
	}
	if got := a.windowTitle(); got != "(1) omaterm" {
		t.Errorf("title %q", got)
	}
}

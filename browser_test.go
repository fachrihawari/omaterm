package main

import (
	"strings"
	"testing"
	"time"

	"omaterm/internal/proto"
)

// The page script carries its arguments as JSON: a selector with quotes
// or a closing script tag stays an argument.
func TestPageScriptEscapes(t *testing.T) {
	s := pageScript(proto.BrowserParams{Op: "click", Selector: `a[title="x"]</script>`}, time.Second)
	if !strings.Contains(s, `"selector":"a[title=\"x\"]\u003c/script\u003e"`) {
		t.Fatalf("%s", s[len(s)-120:])
	}
}

// Driving a page asks the user's leave: Let Agents Control the App.
func TestBrowseNeedsRemoteControl(t *testing.T) {
	a := newApp()
	a.win = nil
	var err error
	a.browse(proto.BrowserParams{ID: "x", Op: "text"}, func(_ *proto.BrowserResult, e error) { err = e })
	if proto.CodeOf(err) != proto.CodeRemoteControl {
		t.Fatalf("%v", err)
	}
}

// A browser pane splits beside the focused one, runs no shell, and is
// saved with its page.
func TestOpenBrowserPane(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	first := a.activeSplitLeaf()
	a.openBrowser("localhost:5173")
	ids := a.activeSplit().termIDs()
	if len(ids) != 2 || ids[0] != first {
		t.Fatalf("panes %v", ids)
	}
	id := ids[1]
	if !a.isBrowser(id) || a.paneURLs[id] != "http://localhost:5173" {
		t.Fatalf("url %q", a.paneURLs[id])
	}
	st := a.state()
	found := false
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			if p.ID == id {
				found = p.URL == "http://localhost:5173" && p.Dir == ""
			}
		}
	}
	if !found {
		t.Fatal("browser pane not saved with its page")
	}
	a.removePane(id)
	if a.isBrowser(id) {
		t.Fatal("closed browser pane kept its page")
	}
}

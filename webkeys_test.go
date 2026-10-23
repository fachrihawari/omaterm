package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestPageKeyCommands(t *testing.T) {
	keys := pageKeyCommands()
	for chord, want := range map[string]string{
		"c:KeyL": "browser.focusAddress", "c:KeyF": "browser.find", "a:ArrowLeft": "browser.back",
		"cs:KeyP": "app.commandPalette", "cs:KeyJ": "session.attention",
	} {
		if keys[chord] != want {
			t.Errorf("%s = %q, want %q", chord, keys[chord], want)
		}
	}
	if _, ok := keys["c:KeyS"]; ok {
		t.Error("an editor binding reached pages")
	}
}

// Only the web view's own secret runs a command; every omaterm-key:
// navigation is cancelled, and others go on.
func TestPageKeySecret(t *testing.T) {
	ev := func(url string) *mygo.NavigateEvent { return &mygo.NavigateEvent{URL: url} }
	e := ev("omaterm-key:s3cret/browser.reload")
	if id, ok := pageKey(e, "s3cret"); !ok || id != "browser.reload" || !e.DefaultPrevented() {
		t.Fatalf("own secret: %q %v prevented=%v", id, ok, e.DefaultPrevented())
	}
	e = ev("omaterm-key:guess/browser.reload")
	if _, ok := pageKey(e, "s3cret"); ok || !e.DefaultPrevented() {
		t.Fatal("a forged secret ran a command, or navigated")
	}
	if _, ok := pageKey(ev("omaterm-key:s3cret/file.save"), "s3cret"); ok {
		t.Fatal("a command no page key runs ran")
	}
	e = ev("https://example.com/")
	if _, ok := pageKey(e, "s3cret"); ok || e.DefaultPrevented() {
		t.Fatal("a page's own navigation was taken")
	}
	if !strings.Contains(pageKeysScript("s3cret"), `"omaterm-key:s3cret/"`) {
		t.Fatal("script lacks its secret")
	}
}

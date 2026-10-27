package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestPageKeyCommands(t *testing.T) {
	for mac, want := range map[bool]map[string]string{
		false: {
			"c:KeyL": "browser.focusAddress", "c:KeyF": "browser.find", "a:ArrowLeft": "browser.back",
			"cs:KeyP": "app.commandPalette", "cs:KeyJ": "session.attention", "c:Comma": "app.settings",
			"cs:KeyD": "terminal.splitDown", "a:Digit1": "tab.select1",
		},
		true: {
			"m:KeyL": "browser.focusAddress", "m:KeyW": "tab.close", "m:BracketLeft": "browser.back",
			"m:BracketRight": "browser.forward", "sm:KeyP": "app.commandPalette", "m:Comma": "app.settings",
			"m:Digit1": "tab.select1", "c:Digit2": "project.select2",
		},
	} {
		keys := pageChords(keybindingsFor(mac))
		for chord, id := range want {
			if keys[chord] != id {
				t.Errorf("mac=%v %s = %q, want %q", mac, chord, keys[chord], id)
			}
		}
		for _, chord := range []string{"c:KeyS", "m:KeyS", "a:ArrowUp"} {
			if id, ok := keys[chord]; ok {
				t.Errorf("mac=%v: %s reached pages as %s", mac, chord, id)
			}
		}
		if mac {
			// ⌥← moves by words in a page's fields, and ⌥1 types.
			for _, chord := range []string{"a:ArrowLeft", "a:ArrowRight", "a:Digit1", "as:Digit1"} {
				if id, ok := keys[chord]; ok {
					t.Errorf("mac: %s reached pages as %s", chord, id)
				}
			}
		}
	}
	// A letter is the layout's, so AZERTY's Ctrl+Z, where QWERTY has W,
	// undoes.
	if !strings.Contains(pageKeysScript("x"), `"Key" + e.key.toUpperCase()`) {
		t.Error("page keys match letters by position")
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

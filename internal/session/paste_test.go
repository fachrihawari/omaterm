package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What is typed can't end the paste, nor act; without bracketed pastes,
// it goes on one line.
func TestPaste(t *testing.T) {
	if got := string(Paste("a\x1b[201~b\x07c\u009bd\r\ne\tf", true)); got != "\x1b[200~a[201~bcd\ne\tf\x1b[201~" {
		t.Errorf("bracketed %q", got)
	}
	if got := string(Paste("one\ntwo  three\n", false)); got != "one two three" {
		t.Errorf("flat %q", got)
	}
}

// The omaterm command's typing is no user's: it leaves no mark of the
// user typing, nor a draft.
func TestSendIsNotTyping(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `sleep 5`)
	if err := p.Send(context.Background(), "echo hi", false); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "a prompt", true); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	typed := p.typed
	p.mu.Unlock()
	if !typed.IsZero() {
		t.Fatal("a send counted as the user typing")
	}
}

// A send waits while the user types in the pane, then gives up saying
// so; once they stop, it goes.
func TestSendHoldsForTyping(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `sleep 5`)
	p.Write([]byte("hel"))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var held HoldError
	if err := p.Send(ctx, "y", false); !errors.As(err, &held) || !strings.Contains(err.Error(), "typing") {
		t.Fatalf("sent while the user typed: %v", err)
	}
	start := time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := p.Send(ctx, "y", false); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatal("went before the user stopped typing")
	}
}

// A paste waits for a draft the user left, which keys needn't.
func TestSendPasteHoldsForDraft(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `sleep 5`)
	p.Write([]byte("hel"))
	p.mu.Lock()
	p.typed = time.Now().Add(-3 * time.Second) // typed a while ago, Enter never came
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := p.Send(ctx, "a prompt", true); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("pasted over a draft: %v", err)
	}
	if err := p.Send(ctx, "y", false); err != nil {
		t.Fatalf("keys held for a draft: %v", err)
	}
}

// A paste is bracketed when the program takes those, and Enter follows
// it after a gap.
func TestSendPasteBracketed(t *testing.T) {
	r := NewRegistry()
	log := filepath.Join(t.TempDir(), "in.log")
	p := startExec(t, r, "p1", "s1", `printf '\033[?2004h'; cat >> `+log)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		p.mu.Lock()
		on := p.scan.Bracketed
		p.mu.Unlock()
		if on {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bracketed paste never came on")
		}
	}
	if err := p.Send(context.Background(), "hello\nthere", true); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[200~hello\nthere\x1b[201~\n" // the Enter, as the tty's line discipline hands it over
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(log)
		if string(b) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program got %q, want %q", b, want)
		}
	}
}

// A user's paste keeps its lines, as a terminal sends them (CR), when the
// program didn't ask for bracketed pastes; every other control is out. The
// agents' Paste still flattens.
func TestUserPaste(t *testing.T) {
	for _, tt := range []struct {
		name, in  string
		bracketed bool
		want      string
	}{
		{"lines kept", "a\nb\r\nc\n", false, "a\rb\rc\r"},
		{"controls and tabs out", "x\ty\x1b[31m\x07z\u009b", false, "xy[31mz"},
		{"indent kept", "if x:\n    y\n", false, "if x:\r    y\r"},
		{"bracketed is as before", "a\x1b[201~b\nc\td", true, "\x1b[200~a[201~b\nc\td\x1b[201~"},
	} {
		if got := string(UserPaste(tt.in, tt.bracketed)); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := string(Paste("a\nb  c\n", false)); got != "a b c" {
		t.Errorf("the agents' paste: %q", got)
	}
}

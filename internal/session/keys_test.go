//go:build linux || darwin

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

// waitInput waits for the pane's program to read what it was sent, in a
// log it appends to, and returns it.
func waitInput(t *testing.T, p *Pane, log, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(log)
		if strings.HasSuffix(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program got %q, want it to end with %q", b, want)
		}
	}
}

// The keyboard modes the program turned on decide how Ctrl+Enter goes:
// the kitty protocol's CSI u, modifyOtherKeys' form, or Ctrl+J.
func TestKeyModes(t *testing.T) {
	cases := []struct {
		name       string
		seq        string
		kitty, mok int
		want       string
	}{
		{"plain", "", 0, 0, "\n"},
		{"kitty", `\033[>1u`, 1, 0, "\x1b[13;5u"},
		{"kitty popped", `\033[>1u\033[<u`, 0, 0, "\n"},
		{"modifyOtherKeys", `\033[>4;2m`, 0, 2, "\x1b[27;5;13~"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			log := filepath.Join(t.TempDir(), "in.log")
			p := startExec(t, r, "p1", "s1", `stty raw -echo; printf 'ready\n' >> `+log+`; printf '`+c.seq+`'; exec cat >> `+log)
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				if b, _ := os.ReadFile(log); strings.Contains(string(b), "ready") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the program never started")
				}
			}
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				p.mu.Lock()
				kitty, mok := p.scan.Kitty, p.scan.ModifyOtherKeys
				p.mu.Unlock()
				if kitty == c.kitty && mok == c.mok {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("modes %d, %d, want %d, %d", kitty, mok, c.kitty, c.mok)
				}
			}
			if err := p.Key("ctrl+enter"); err != nil {
				t.Fatal(err)
			}
			waitInput(t, p, log, c.want)
		})
	}
}

// A paste is bracketed when the program takes bracketed pastes, and flat
// otherwise.
func TestPasteText(t *testing.T) {
	cases := []struct {
		name, seq, want string
	}{
		{"bracketed", `printf '\033[?2004h'; `, "\x1b[200~hi\nthere\x1b[201~"},
		{"plain", "", "hi\rthere"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			log := filepath.Join(t.TempDir(), "in.log")
			p := startExec(t, r, "p1", "s1", `stty raw -echo; printf 'ready\n' >> `+log+`; `+c.seq+`exec cat >> `+log)
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				b, _ := os.ReadFile(log)
				p.mu.Lock()
				ready := strings.Contains(string(b), "ready") && p.scan.Bracketed == (c.seq != "")
				p.mu.Unlock()
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the program never started")
				}
			}
			if err := p.PasteText("hi\nthere"); err != nil {
				t.Fatal(err)
			}
			waitInput(t, p, log, c.want)
		})
	}
}

// The omaterm command's keys wait while the user types, fail saying so, and
// are no user's typing; the user's own keys go at once, and count.
func TestKeysForAgentsWaitAndAreNotTyping(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `sleep 5`)
	p.Write([]byte("hel"))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var held HoldError
	if err := p.SendKeys(ctx, []string{"enter"}); !errors.As(err, &held) || !strings.Contains(err.Error(), "typing") {
		t.Fatalf("keys sent while the user typed: %v", err)
	}

	// A pane nobody types in: the keys go at once and leave no mark.
	q := startExec(t, r, "p2", "s2", `sleep 5`)
	if err := q.SendKeys(context.Background(), []string{"down", "tab"}); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	typed := q.typed
	q.mu.Unlock()
	if !typed.IsZero() {
		t.Fatal("keys for the omaterm command counted as the user typing")
	}
	// A bad name sends nothing, and says so.
	if err := q.SendKeys(context.Background(), []string{"enter", "nonsense-key"}); err == nil {
		t.Fatal("a bad key name was sent")
	}

	// The user's key is not held, and is their typing: the next send waits.
	start := time.Now()
	if err := p.Key("down"); err != nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("the user's key: %v after %v", err, time.Since(start))
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if err := q.Key("down"); err != nil {
		t.Fatal(err)
	}
	if err := q.Send(ctx2, "x", false); !errors.As(err, &held) {
		t.Fatalf("a send right after the user's key: %v", err)
	}
}

// The agents' keys encode with the program's modes too.
func TestSendKeysUseTheProgramsModes(t *testing.T) {
	r := NewRegistry()
	log := filepath.Join(t.TempDir(), "in.log")
	p := startExec(t, r, "p1", "s1", `stty raw -echo; printf 'ready\n' >> `+log+`; printf '\033[>1u'; exec cat >> `+log)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the program never started")
		}
	}
	waitFor(t, p, "kitty on", func(Info) bool { p.mu.Lock(); defer p.mu.Unlock(); return p.scan.Kitty == 1 })
	if err := p.SendKeys(context.Background(), []string{"ctrl+enter"}); err != nil {
		t.Fatal(err)
	}
	waitInput(t, p, log, "\x1b[13;5u")
}

// An Enter right after a paste waits out the rest of the gap, so the program
// reads the paste first; an Enter with no paste before it, or with a
// modifier, does not wait.
func TestEnterAfterPasteWaits(t *testing.T) {
	r := NewRegistry()
	log := filepath.Join(t.TempDir(), "in.log")
	p := startExec(t, r, "p1", "s1", `stty raw -echo; printf 'ready\n' >> `+log+`; exec cat >> `+log)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the program never started")
		}
	}
	timed := func(name string) time.Duration {
		start := time.Now()
		if err := p.Key(name); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	if d := timed("enter"); d > pasteGap/2 {
		t.Fatalf("an Enter with no paste before it took %v", d)
	}
	if err := p.PasteText("echo hi"); err != nil {
		t.Fatal(err)
	}
	if d := timed("ctrl+enter"); d > pasteGap/2 {
		t.Fatalf("Ctrl+Enter waited %v", d)
	}
	if err := p.PasteText("echo hi"); err != nil {
		t.Fatal(err)
	}
	if d := timed("Enter"); d < pasteGap*2/3 || d > 3*pasteGap {
		t.Fatalf("an Enter right after a paste took %v, want about %v", d, pasteGap)
	}
	// Much later, no wait.
	time.Sleep(pasteGap + 50*time.Millisecond)
	if d := timed("enter"); d > pasteGap/2 {
		t.Fatalf("an Enter long after a paste took %v", d)
	}
}

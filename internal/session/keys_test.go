package session

import (
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
		{"plain", "", "hi there"},
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

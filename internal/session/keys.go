package session

import (
	"context"
	"strings"
	"time"

	"omaterm/internal/keys"
)

// modes are the keyboard modes the program in the pane asked for.
func (p *Pane) modes() keys.Modes {
	p.mu.Lock()
	defer p.mu.Unlock()
	return keys.Modes{Kitty: p.scan.Kitty, ModifyOtherKeys: p.scan.ModifyOtherKeys}
}

// Key sends a key, by name, as the user's typing: the app's and the web
// page's own keys. The program's keyboard modes decide how a key with
// modifiers is encoded: Enter with Ctrl is a newline on a plain terminal,
// and CSI 13;5u when the program asked for keys to be disambiguated. An
// Enter right after a paste waits out the rest of pasteGap, as the
// program reads the paste first.
func (p *Pane) Key(name string) error {
	b, err := keys.Bytes(name, p.modes())
	if err != nil {
		return err
	}
	if n := strings.ToLower(strings.TrimSpace(name)); n == "enter" || n == "return" {
		p.mu.Lock()
		wait := pasteGap - time.Since(p.pastedAt)
		p.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	return p.Write(b)
}

// SendKeys sends keys, by name, for the omaterm command: no user's typing,
// so it waits while the user types in the pane, as Send does, and fails
// busy after the same time. Every name is encoded first: a bad one sends
// none.
func (p *Pane) SendKeys(ctx context.Context, names []string) error {
	m := p.modes()
	var data []byte
	for _, name := range names {
		b, err := keys.Bytes(name, m)
		if err != nil {
			return err
		}
		data = append(data, b...)
	}
	return p.Send(ctx, string(data), false)
}

// PasteText pastes text as the user's typing, bracketed when the program
// takes bracketed pastes, else with its lines as a terminal sends them:
// no send hold applies, as they are the one typing it.
func (p *Pane) PasteText(text string) error {
	p.mu.Lock()
	bracketed := p.scan.Bracketed
	p.pastedAt = time.Now()
	p.mu.Unlock()
	return p.Write(UserPaste(text, bracketed))
}

package session

import "omaterm/internal/keys"

// Key sends a key, by name, as the user's typing. The program's keyboard
// modes decide how a key with modifiers is encoded: Enter with Ctrl is a
// newline on a plain terminal, and CSI 13;5u when the program asked for
// keys to be disambiguated.
func (p *Pane) Key(name string) error {
	p.mu.Lock()
	m := keys.Modes{Kitty: p.scan.Kitty, ModifyOtherKeys: p.scan.ModifyOtherKeys}
	p.mu.Unlock()
	b, err := keys.Bytes(name, m)
	if err != nil {
		return err
	}
	return p.Write(b)
}

// PasteText pastes text as the user's typing, bracketed when the program
// takes bracketed pastes: no send hold applies, as they are the one
// typing it.
func (p *Pane) PasteText(text string) error {
	p.mu.Lock()
	bracketed := p.scan.Bracketed
	p.mu.Unlock()
	return p.Write(Paste(text, bracketed))
}

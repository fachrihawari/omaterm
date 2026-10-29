package session

import (
	"context"
	"io"
	"strings"
	"time"
)

// Automated typing into a pane (omaterm send and prompt, the mailbox):
// it waits for the user to finish typing there, and never counts as
// their typing.
const (
	// pasteGap is between a pasted text and the Enter that sends it: the
	// program reads the paste first.
	pasteGap = 150 * time.Millisecond
	// sendHold is how long a send waits for the user, at most.
	sendHold = 10 * time.Second
)

// HoldError is Send's error for a send that waited for the user in vain:
// why it waited.
type HoldError string

func (e HoldError) Error() string { return string(e) }

// Paste is text as typed into a prompt: bracketed as a paste, when the
// program takes those, else on one line, as a newline would send it. No
// control in it can end the paste, or act.
func Paste(text string, bracketed bool) []byte {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, strings.ReplaceAll(text, "\r\n", "\n"))
	if !bracketed {
		return []byte(strings.Join(strings.Fields(text), " "))
	}
	return []byte("\x1b[200~" + text + "\x1b[201~")
}

// Send types data into the program for the omaterm command: keys as they
// are, or as a paste into a prompt with Enter after it. It waits, as long
// as ctx and sendHold allow, while the user types in the pane (and, for a
// paste, while they left a draft there), then fails with the reason. It
// is no user's typing: it only dismisses what a new turn makes old.
func (p *Pane) Send(ctx context.Context, data string, paste bool) error {
	ctx, cancel := context.WithTimeout(ctx, sendHold)
	defer cancel()
	var bracketed bool
	for {
		p.mu.Lock()
		hold := p.userHold(time.Now(), paste)
		bracketed = p.scan.Bracketed
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return io.ErrClosedPipe
		}
		if hold == "" {
			break
		}
		select {
		case <-ctx.Done():
			return HoldError(hold)
		case <-time.After(100 * time.Millisecond):
		}
	}
	p.input()
	b := []byte(data)
	if paste {
		b = Paste(data, bracketed)
	}
	if err := p.write(b); err != nil || !paste {
		return err
	}
	time.Sleep(pasteGap)
	return p.write([]byte("\r"))
}

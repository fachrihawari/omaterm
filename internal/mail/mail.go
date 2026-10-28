// Package mail carries messages between the agents omaterm runs: an agent
// asks another a question (omaterm ask), which omaterm types into that
// agent's prompt once it is free, and the answer (omaterm reply) comes
// back to the asker, waiting for it or not.
//
// The mailbox lives with the panes: in the session daemon, or without one
// in the app. It is kept in memory only.
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"omaterm/internal/proto"
	"omaterm/internal/session"
)

const (
	// MaxText bounds a message, and an answer.
	MaxText = 64 << 10
	// bootHold is how long a message waits for an agent in a pane that
	// runs none yet, as one that just started.
	bootHold = 30 * time.Second
	// quietGrace is how long an agent stays stopped, its message
	// unanswered, before it is taken to have stopped without replying.
	quietGrace = 3 * time.Second
	// enterAgain is when Enter is pressed again, once, for an agent that
	// didn't start on a message: the first may have gone with the paste;
	// startLimit when one that still didn't is taken to have dropped it.
	enterAgain = 3 * time.Second
	startLimit = 2 * time.Minute
	// abandoned is when an ask nobody waits for has its answer go to the
	// asker as a message: its command went away before it waited.
	abandoned = 10 * time.Second
	// pasteGap is between a message's text and the Enter that sends it:
	// an agent that reads a paste whole takes an Enter in it as a newline.
	pasteGap = 150 * time.Millisecond
	// kept is how many messages omaterm inbox shows.
	kept = 200
)

// Mailbox holds the messages for the panes of a registry, and types each
// into its agent's prompt once it is free (Run).
type Mailbox struct {
	reg *session.Registry
	now func() time.Time

	mu      sync.Mutex
	next    int
	items   []*item
	changed chan struct{} // closed and replaced on every change
	// limits are how many more times a pane may ask another (Limit).
	limits map[[2]string]int
}

// item is a message, with what the mailbox knows of its way.
type item struct {
	proto.Mail
	waiters int
	// released sends its outcome to the asker as a message: nobody waits
	// for it; left is when the last one stopped (or it was sent).
	released bool
	left     time.Time
	told     bool      // its outcome went to the asker
	sending  bool      // being typed
	at       time.Time // when it was typed
	again    bool      // Enter was pressed again
}

// New returns a mailbox for reg's panes.
func New(reg *session.Registry) *Mailbox {
	return &Mailbox{reg: reg, now: time.Now, changed: make(chan struct{}), limits: map[[2]string]int{}}
}

// settled tells a message whose way is over: answered, or not to be, or
// a note typed in.
func (it *item) settled() bool {
	switch it.State {
	case proto.MailReplied, proto.MailNoReply, proto.MailFailed:
		return true
	}
	return it.Note && it.State == proto.MailDelivered
}

func (it *item) open() bool {
	return it.State == proto.MailQueued || it.State == proto.MailDelivered
}

// to is who a message is for, as told to its asker.
func (it *item) to() string { return named(it.ToName, it.To) }

func named(name, pane string) string {
	if name != "" {
		return name
	}
	return "pane " + pane
}

func (m *Mailbox) changedLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func (m *Mailbox) find(id string) *item {
	for _, it := range m.items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func checkText(text, what string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return proto.Errorf(proto.CodeUsage, "an empty "+what)
	case len(text) > MaxText:
		return proto.Errorf(proto.CodeUsage, "a "+what+" is at most 64 KiB: put the rest in a file, and name it")
	}
	return nil
}

// Ask queues a message for the agent in pane p.To.
func (m *Mailbox) Ask(p proto.MailAskParams) (proto.Mail, error) {
	if err := checkText(p.Text, "message"); err != nil {
		return proto.Mail{}, err
	}
	if p.From != "" && p.From == p.To {
		return proto.Mail{}, proto.Errorf(proto.CodeUsage, "that is your own pane")
	}
	if m.reg.Get(p.To) == nil {
		return proto.Mail{}, proto.Errorf(proto.CodeNotFound, "no pane "+p.To)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !p.Note && p.From != "" {
		// Its agent waits for the asker's answer: an ask back would wait
		// for it, which can't answer while it waits.
		for _, it := range m.items {
			if it.From == p.To && it.To == p.From && it.open() && !it.Note && it.waiters > 0 {
				return proto.Mail{}, proto.Errorf(proto.CodeUsage, fmt.Sprintf(
					"%s is waiting for your answer to %s: give it with omaterm reply %s", named(p.ToName, p.To), it.ID, it.ID))
			}
		}
	}
	if n, ok := m.limits[[2]string{p.From, p.To}]; ok && !p.Note {
		if n <= 0 {
			return proto.Mail{}, proto.Errorf(proto.CodeUsage, "you asked "+named(p.ToName, p.To)+
				" as many times as you may: tell the user what you found, with omaterm report done")
		}
		m.limits[[2]string{p.From, p.To}] = n - 1
	}
	now := m.now()
	m.next++
	it := &item{Mail: proto.Mail{ID: "m" + strconv.Itoa(m.next), From: p.From, FromName: p.FromName, To: p.To, ToName: p.ToName,
		Text: p.Text, Note: p.Note, State: proto.MailQueued, Created: now.UnixMilli()},
		released: p.NoWait || p.Note, left: now}
	m.items = append(m.items, it)
	m.trim()
	m.changedLocked()
	return it.Mail, nil
}

// Limit lets pane from ask pane to at most n times more: a review's
// rounds.
func (m *Mailbox) Limit(from, to string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits[[2]string{from, to}] = n
}

// trim forgets the oldest messages past kept that are over.
func (m *Mailbox) trim() {
	over := len(m.items) - kept
	if over <= 0 {
		return
	}
	keep := m.items[:0]
	for _, it := range m.items {
		if over > 0 && it.settled() && it.waiters == 0 && (it.told || !it.released || it.Note) {
			over--
			continue
		}
		keep = append(keep, it)
	}
	m.items = keep
}

// errReleased ends a wait whose waiter went away (Release).
var errReleased = errors.New("released")

// Await waits for a message's answer, or for its way to be over
// otherwise. The answer of one no longer waited for goes to its asker as
// a message.
func (m *Mailbox) Await(ctx context.Context, id string) (proto.Mail, error) {
	m.mu.Lock()
	it := m.find(id)
	if it == nil {
		m.mu.Unlock()
		return proto.Mail{}, proto.Errorf(proto.CodeNotFound, "no message "+id)
	}
	it.waiters++
	it.released = false
	defer func() {
		m.mu.Lock()
		if it.waiters--; it.waiters == 0 {
			it.left = m.now()
			it.released = it.released || !it.settled()
		}
		m.mu.Unlock()
	}()
	for {
		mail, ch := it.Mail, m.changed
		switch {
		case it.settled():
			m.mu.Unlock()
			return mail, nil
		case it.released:
			m.mu.Unlock()
			return mail, errReleased
		}
		m.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return mail, ctx.Err()
		}
		m.mu.Lock()
	}
}

// Release stops the waits for a message's answer, which then goes to its
// asker as a message: the command waiting went away, as one the app
// passed on to the session daemon.
func (m *Mailbox) Release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it := m.find(id); it != nil && !it.settled() {
		it.released = true
		m.changedLocked()
	}
}

// Reply answers a message to the agent in pane p.From: p.ID, else the
// last one it was given. With no From (a command outside omaterm), any.
func (m *Mailbox) Reply(p proto.MailReplyParams) (proto.Mail, error) {
	if err := checkText(p.Text, "answer"); err != nil {
		return proto.Mail{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var it *item
	if p.ID == "" {
		for i := len(m.items) - 1; i >= 0 && it == nil; i-- {
			x := m.items[i]
			answerable := x.State == proto.MailDelivered || x.State == proto.MailNoReply || x.sending
			if x.To == p.From && !x.Note && answerable {
				it = x
			}
		}
		if it == nil {
			return proto.Mail{}, proto.Errorf(proto.CodeNotFound, "no message waits for your answer")
		}
	} else if it = m.find(p.ID); it == nil {
		return proto.Mail{}, proto.Errorf(proto.CodeNotFound, "no message "+p.ID)
	}
	switch {
	case p.From != "" && it.To != p.From:
		return proto.Mail{}, proto.Errorf(proto.CodeUsage, it.ID+" was for "+it.to()+", not for you")
	case it.Note:
		return proto.Mail{}, proto.Errorf(proto.CodeUsage, it.ID+" wants no answer")
	case it.State == proto.MailReplied:
		return proto.Mail{}, proto.Errorf(proto.CodeUsage, it.ID+" has its answer already")
	case it.State == proto.MailQueued && !it.sending, it.State == proto.MailFailed:
		// Being typed, its agent may answer it at once.
		return proto.Mail{}, proto.Errorf(proto.CodeUsage, it.ID+" wasn't delivered")
	}
	if it.State == proto.MailNoReply && it.waiters == 0 {
		// A late answer: its asker was told there was none, and gets it.
		it.released, it.told = true, false
	}
	it.State, it.Reply = proto.MailReplied, p.Text
	m.changedLocked()
	return it.Mail, nil
}

// List lists the messages to and from pane, every one for "".
func (m *Mailbox) List(pane string) []proto.Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []proto.Mail{}
	for _, it := range m.items {
		if pane == "" || it.From == pane || it.To == pane {
			out = append(out, it.Mail)
		}
	}
	return out
}

// Run types the messages into their agents' prompts as they are free,
// and follows what the agents do with them, until ctx ends.
func (m *Mailbox) Run(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick(m.now())
		}
	}
}

// Tick moves each message on its way, as of now.
func (m *Mailbox) Tick(now time.Time) {
	type send struct {
		it    *item
		p     *session.Pane
		b     []byte
		enter bool // Enter again, only
	}
	var sends []send
	m.mu.Lock()
	changed := false
	inbox, held, asking := map[string]int{}, map[string]string{}, map[string]string{}
	var outcomes []*item
	for _, it := range m.items {
		before := it.Mail
		p := m.reg.Get(it.To)
		switch {
		case it.State == proto.MailQueued && !it.sending:
			if p == nil {
				it.State, it.Why = proto.MailFailed, it.to()+" closed"
				break
			}
			hold, bracketed := p.MailHold(now)
			switch {
			case hold == session.NoAgent && now.Sub(p.Started()) > bootHold:
				it.State, it.Why = proto.MailFailed, "no agent runs in "+it.to()+": omaterm send types into a shell"
			case hold != "":
				it.Why = hold
				inbox[it.To]++
				held[it.To] = hold
			default:
				it.sending, it.at, it.Why = true, now, ""
				sends = append(sends, send{it: it, p: p, b: paste(m.wrap(it), bracketed)})
			}
		case it.State == proto.MailDelivered && !it.Note:
			if p == nil {
				it.State, it.Why = proto.MailNoReply, it.to()+" closed"
				break
			}
			info := p.Info()
			worked := info.Status == session.Working || info.Since >= it.at.UnixMilli()
			stopped := info.Status != session.Working && info.Status != session.Waiting
			switch {
			case !worked && !it.again && now.Sub(it.at) > enterAgain:
				it.again = true
				sends = append(sends, send{it: it, p: p, b: []byte("\r"), enter: true})
			case worked && stopped && now.Sub(time.UnixMilli(info.Since)) >= quietGrace:
				it.State, it.Screen = proto.MailNoReply, p.Text(false, 40)
			case !worked && now.Sub(it.at) > startLimit:
				it.State, it.Why, it.Screen = proto.MailNoReply, "it didn't start on it", p.Text(false, 40)
			}
		}
		if it.open() && !it.Note && it.From != "" && it.waiters > 0 {
			asking[it.From] = it.To
		}
		if !it.released && it.waiters == 0 && !it.settled() && now.Sub(it.left) > abandoned {
			it.released = true
		}
		if it.released && it.settled() && !it.Note && it.From != "" && !it.told {
			it.told = true
			outcomes = append(outcomes, it)
		}
		changed = changed || it.Mail != before
	}
	for _, it := range outcomes {
		if m.reg.Get(it.From) == nil {
			continue
		}
		m.next++
		m.items = append(m.items, &item{Mail: proto.Mail{ID: "m" + strconv.Itoa(m.next), To: it.From, ToName: it.FromName,
			Text: outcome(it), Note: true, State: proto.MailQueued, Created: now.UnixMilli()}, released: true, left: now})
		changed = true
	}
	if changed {
		m.trim()
		m.changedLocked()
	}
	m.mu.Unlock()
	m.reg.Each(func(p *session.Pane) {
		id := p.ID()
		p.SetMail(inbox[id], held[id], asking[id])
	})
	for _, s := range sends {
		go m.send(s.it, s.p, s.b, s.enter)
	}
}

// send types a message into its agent's prompt, then Enter; or Enter only.
func (m *Mailbox) send(it *item, p *session.Pane, b []byte, enter bool) {
	err := p.Deliver(b)
	if err == nil && !enter {
		time.Sleep(pasteGap)
		err = p.Deliver([]byte("\r"))
	}
	if enter {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it.sending = false
	switch {
	case it.State != proto.MailQueued: // answered as it was typed
	case err != nil:
		it.State, it.Why = proto.MailFailed, "it couldn't be typed into "+it.to()+": "+err.Error()
	default:
		it.State = proto.MailDelivered
	}
	m.changedLocked()
}

// wrap is a message as its agent reads it: who it is from, and how to
// answer it; a note of omaterm's is whole as it is.
func (m *Mailbox) wrap(it *item) string {
	if it.Note {
		return it.Text
	}
	from := fmt.Sprintf("the agent in session %q (pane %s)", it.FromName, it.From)
	if it.From == "" {
		from = "a command run outside omaterm"
	}
	return fmt.Sprintf("[omaterm] Message %s from %s, not from the user:\n%s\n\nAnswer it with: omaterm reply %s \"…\" (or --file <path>)",
		it.ID, from, strings.TrimSpace(it.Text), it.ID)
}

// outcome tells an asker who no longer waited what came of its message.
func outcome(it *item) string {
	switch it.State {
	case proto.MailReplied:
		return fmt.Sprintf("[omaterm] %s's answer to your message %s:\n%s", it.to(), it.ID, strings.TrimSpace(it.Reply))
	case proto.MailNoReply:
		if it.Screen == "" {
			return fmt.Sprintf("[omaterm] %s stopped without answering your message %s: %s.", it.to(), it.ID, it.Why)
		}
		return fmt.Sprintf("[omaterm] %s stopped without answering your message %s. The end of its screen:\n%s",
			it.to(), it.ID, strings.TrimRight(it.Screen, "\n"))
	}
	return fmt.Sprintf("[omaterm] Your message %s to %s wasn't delivered: %s.", it.ID, it.to(), it.Why)
}

// paste is text as typed into a prompt: bracketed as a paste, when the
// program takes those, else on one line, as a newline would send it. No
// control in it can end the paste, or act.
func paste(text string, bracketed bool) []byte {
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

// IsMethod tells the socket's methods Serve answers.
func IsMethod(method string) bool { return strings.HasPrefix(method, "mail.") }

// Serve answers the omaterm command's mail methods.
func (m *Mailbox) Serve(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		if err := json.Unmarshal(raw, v); err != nil {
			return proto.Errorf(proto.CodeUsage, err.Error())
		}
		return nil
	}
	switch method {
	case "mail.ask":
		var p proto.MailAskParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return m.Ask(p)
	case "mail.await":
		var p proto.MailAwaitParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		if p.TimeoutMS > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
			defer cancel()
		}
		mail, err := m.Await(ctx, p.ID)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errReleased) {
			return nil, proto.Errorf(proto.CodeTimeout, "no answer from "+named(mail.ToName, mail.To)+
				" yet: it comes to you as a message once there is one")
		}
		return mail, err
	case "mail.release":
		var p proto.IDParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		m.Release(p.ID)
		return true, nil
	case "mail.reply":
		var p proto.MailReplyParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return m.Reply(p)
	case "mail.limit":
		var p proto.MailLimitParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		m.Limit(p.From, p.To, p.N)
		return true, nil
	case "mail.list":
		var p proto.MailListParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return m.List(p.Pane), nil
	}
	return nil, proto.Errorf(proto.CodeUsage, "no method "+method)
}

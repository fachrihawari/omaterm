// Partly adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/session.go.

package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/agent"
	"omaterm/internal/detect"
	"omaterm/internal/osc"
	"omaterm/internal/pty"
	"omaterm/internal/shellcmd"
)

// idleHold: an agent's screen that stops showing work, without showing its
// prompt either, is idle once it has stayed so this long: a turn's spinner
// line can go between two redraws.
const idleHold = 700 * time.Millisecond

// Pane is a shell the app runs in a pseudo-terminal, with the terminal
// emulator that shows it.
type Pane struct {
	reg *Registry

	// mu guards the fields below. Lock order: mu before the terminal's own
	// lock; the terminal never calls back with its lock held.
	mu      sync.Mutex
	info    Info
	proc    *pty.Proc
	term    *terminal.Terminal
	changed chan struct{} // closed and replaced on every change

	scan     osc.Scanner
	recs     osc.Records
	reported bool
	progress string // the last OSC 9;4 after "9;", as "4;1;40"
	awaiting bool   // the start command runs; OSC 133;D tells its end

	// seen is what the screen showed when the status last came from it,
	// which a status of other origin then stands against until the screen
	// shows something else; looked tells the screen was read since it
	// last changed; idleFrom holds back working going idle (idleHold).
	seen     detect.State
	looked   bool
	idleFrom time.Time
	cwdAt    time.Time
	lastBell time.Time
	rows     int
	closed   bool
	dirty    bool // output since History was last taken

	// For the mailbox: typed is when the user last typed in the pane
	// (keys: not the terminal's answers, nor focus or mouse reports), and
	// typedEnd whether that ended what they typed (Enter, Ctrl+C, Escape)
	// or may have left a draft; started is when the pane started; prompt
	// tells that the screen last showed the agent's prompt outright.
	typed    time.Time
	typedEnd bool
	started  time.Time
	prompt   bool

	env []string // what the pane was started with, for a restart
}

// StartOptions are how Registry.Start runs a pane.
type StartOptions struct {
	ID, Session, Project string
	// ProjectName and ProjectDir tell programs the project they run in
	// (OMATERM_PROJECT, OMATERM_PROJECT_DIR); not set, they aren't told.
	ProjectName, ProjectDir string
	// Dir is where the shell starts; the home directory when empty.
	Dir string
	// Command runs first, in the user's login shell, which the pane goes
	// on with once it ends. Exec runs Command itself instead, with no
	// shell (tests).
	Command []string
	Exec    bool
	// Env adds to the environment, as "KEY=value".
	Env        []string
	Cols, Rows int

	// The terminal's look.
	Font            terminal.Font
	Theme           *terminal.Theme
	MinimumContrast float32
	Padding         terminal.Padding
	// The terminal's behavior, as the app's settings have it (see
	// terminal.Options): what has no setter takes effect as a terminal
	// is made.
	Cursor        terminal.CursorStyle
	NoBlink       bool
	CopyOnSelect  terminal.CopyOnSelect
	RightClick    terminal.RightClickAction
	ClipboardRead terminal.ClipboardAccess
	Scrollback    int
	OpenLink      func(link string) bool
	OnZoom        func(z terminal.Zoom) bool
	// Shell is the shell to run; the login shell when empty.
	Shell string
	// History is shown above the shell, as a restored pane's scrollback.
	History []byte
}

// Info returns what is known of the pane.
func (p *Pane) Info() Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.info
	i.Records = append([]osc.Record(nil), i.Records...)
	return i
}

// Env is the environment the pane was started with (StartOptions.Env),
// for starting it again.
func (p *Pane) Env() []string {
	return append([]string(nil), p.env...)
}

// ID is the pane's id.
func (p *Pane) ID() string { return p.info.ID }

// Term is the pane's terminal, for a view to show.
func (p *Pane) Term() *terminal.Terminal { return p.term }

// TermIfOpen is Term: a pane in the app always has its terminal.
func (p *Pane) TermIfOpen() *terminal.Terminal { return p.term }

// Changed is closed on the pane's next change of Info.
func (p *Pane) Changed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.changed
}

// changedLocked wakes those waiting on the pane and tells the registry;
// p.mu is held.
func (p *Pane) changedLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
	p.reg.changed()
}

// Write sends what the user typed to the program: the app's keyboard,
// the daemon's attach connections, the web page's. The omaterm command's
// typing goes through Send.
func (p *Pane) Write(b []byte) error {
	p.input()
	p.typing(b)
	return p.write(b)
}

// write sends b to the program.
func (p *Pane) write(b []byte) error {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return io.ErrClosedPipe
	}
	_, err := proc.Write(b)
	return err
}

// Deliver types b for the mailbox (package mail). Unlike Write it is no
// user's typing: it only dismisses the finished reports a new turn makes
// old.
func (p *Pane) Deliver(b []byte) error {
	p.mu.Lock()
	proc, closed := p.proc, p.closed
	if !closed && p.recs.Len() > 0 && p.recs.DismissFinished() {
		p.fromRecords()
		p.changedLocked()
	}
	p.mu.Unlock()
	if closed || proc == nil {
		return io.ErrClosedPipe
	}
	_, err := proc.Write(b)
	return err
}

// The mailbox's holds: the user typed in the pane this recently, or left
// a draft in it this recently; an agent without its prompt in sight is
// taken to be at it once its screen stays idle this long.
const (
	typingHold = 2 * time.Second
	draftHold  = time.Minute
	settleHold = 2 * time.Second
)

// NoAgent is MailHold's word for a pane that runs no agent.
const NoAgent = "no agent runs in it"

// MailHold tells why a message for the pane's agent waits, "" when it can
// go now: an agent is at its prompt, and the user neither types in the
// pane nor left a draft there. bracketed tells to paste it bracketed.
func (p *Pane) MailHold(now time.Time) (hold string, bracketed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.info
	since := time.UnixMilli(i.Since)
	switch {
	case p.closed || i.Status == Exited:
		return "it closed", false
	case !detect.Known(i.Agent):
		return NoAgent, false
	case i.Status == Waiting:
		return "it waits for the user", false
	case i.Status == Working:
		return "it is working", false
	case p.userHold(now, true) != "":
		return p.userHold(now, true), false
	case !p.prompt && detect.ShowsPrompt(i.Agent):
		// Something else shows over it, as a dialog, which what is typed
		// would answer.
		return "it isn't at its prompt", false
	case p.seen != detect.Idle && !p.reported,
		!p.prompt && !p.reported && now.Sub(since) < settleHold,
		now.Sub(time.UnixMilli(i.Activity)) < time.Second:
		return "it is starting", false
	}
	return "", p.scan.Bracketed
}

// userHold tells why automated typing waits for the user, "" when it
// needn't: they typed in the pane just now, or, when draft, left a draft
// there. p.mu is held.
func (p *Pane) userHold(now time.Time, draft bool) string {
	switch {
	case now.Sub(p.typed) < typingHold:
		return "the user is typing in it"
	case draft && !p.typedEnd && now.Sub(p.typed) < draftHold:
		return "the user left a draft in it"
	}
	return ""
}

// Started is when the pane started.
func (p *Pane) Started() time.Time { return p.started }

// SetMail tells what the mailbox holds for the pane (Info.Inbox, Held and
// Asking).
func (p *Pane) SetMail(inbox int, held, asking string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.Inbox != inbox || p.info.Held != held || p.info.Asking != asking {
		p.info.Inbox, p.info.Held, p.info.Asking = inbox, held, asking
		p.changedLocked()
	}
}

// Text returns the pane's text: the screen, or with scrollback; the last
// lines lines when lines > 0.
func (p *Pane) Text(scrollback bool, lines int) string {
	if p.term == nil {
		return ""
	}
	text := p.term.Text()
	p.mu.Lock()
	rows := p.rows
	p.mu.Unlock()
	all := strings.Split(strings.TrimRight(text, "\n "), "\n")
	if !scrollback && lines <= 0 {
		lines = rows
	}
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	for i, l := range all {
		all[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(all, "\n") + "\n"
}

// History returns the scrollback and screen to keep for a restore, nil
// when nothing changed since it was last taken, unless force.
func (p *Pane) History(force bool) []byte {
	p.mu.Lock()
	dirty := p.dirty
	p.dirty = false
	p.mu.Unlock()
	if p.term == nil || !dirty && !force {
		return nil
	}
	return p.term.History()
}

// ErrClosed is Wait's error for a pane closed before until held.
var ErrClosed = errors.New("pane closed")

// Wait waits until until holds for the pane's Info, or ctx ends, or the
// pane is closed: waiting on, `omaterm wait` on a closed pane never
// returned.
func (p *Pane) Wait(ctx context.Context, until func(Info) bool) (Info, error) {
	for {
		p.mu.Lock()
		info, ch, closed := p.info, p.changed, p.closed
		p.mu.Unlock()
		if until(info) {
			return info, nil
		}
		if closed {
			return info, ErrClosed
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return info, ctx.Err()
		}
	}
}

// Report takes a status report (OSC 7501's body after "7501;") as if the
// program wrote it to the terminal: from `omaterm report` run with none,
// as by an agent's commands.
func (p *Pane) Report(body string) error {
	rep, ok := osc.ParseStatus(body)
	if !ok || rep.Query {
		return errors.New("not a status report")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs.Apply(rep.Record)
	p.reported = true
	p.fromRecords()
	p.changedLocked()
	return nil
}

// Flag marks the pane as needing the user, as `omaterm notify` asks.
func (p *Pane) Flag(title, body string) {
	msg := strings.TrimSpace(body)
	if msg == "" {
		msg = strings.TrimSpace(title)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.needsUser(msg)
}

// SetAgentSession records the agent conversation the pane runs, to resume
// after a restart.
func (p *Pane) SetAgentSession(s *agent.Session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.info.AgentSession = s
	p.changedLocked()
}

// SetTurn records where the agent's last turn started, if newer.
func (p *Pane) SetTurn(t *Turn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.Turn != nil && t != nil && p.info.Turn.At >= t.At {
		return
	}
	p.info.Turn = t
	p.changedLocked()
}

// Close hangs up the shell and frees the pane.
func (p *Pane) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	proc := p.proc
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
	if p.term != nil {
		p.term.Close()
	}
	if proc != nil {
		proc.Close()
		go func() {
			select {
			case <-proc.Exited():
			case <-time.After(2 * time.Second):
				proc.Kill()
			}
		}()
	}
}

// --- the pane's terminal connection ---------------------------------------

// conn connects the terminal to the program: what it reads is the
// program's output, scanned on its way; what it writes the user's input
// and the emulator's answers to the program's queries.
type conn struct{ p *Pane }

func (c conn) Read(b []byte) (int, error) {
	n, err := c.p.proc.Read(b)
	if n > 0 {
		c.p.output(b[:n])
	}
	if err == io.EOF {
		c.p.exited()
	}
	return n, err
}

// reply tells the emulator's answers to queries, which the program asked
// for, from what the user typed: focus reports, cursor and device
// reports, color and mode answers.
var reply = regexp.MustCompile(`^\x1b(\[[?>=]?[0-9;]*[RcntIO]|\[\?[0-9;]*[u$]|\[[0-9;?]*\$y|P|\]|_)`)

func (c conn) Write(b []byte) (int, error) {
	if !reply.Match(b) {
		c.p.input()
		c.p.typing(b)
	}
	return c.p.proc.Write(b)
}

func (c conn) Close() error { return c.p.proc.Close() }

func (c conn) ResizePixels(cols, rows, cellW, cellH int) error {
	c.p.mu.Lock()
	c.p.rows = rows
	c.p.mu.Unlock()
	return c.p.proc.Resize(cols, rows, cellW, cellH)
}

// output takes the program's output, before the emulator does.
func (p *Pane) output(b []byte) {
	p.outputLocked(b)
	p.reg.output(p.info.ID)
}

func (p *Pane) outputLocked(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	before := p.info.Status
	var replies [][]byte
	p.scan.Scan(b, func(body []byte) {
		if r := p.handleOSC(body); r != nil {
			replies = append(replies, r)
		}
	}, p.fullReset)
	p.info.Activity = time.Now().UnixMilli()
	p.looked = false
	p.dirty = true
	if p.info.Status != before {
		p.changedLocked()
	}
	if len(replies) > 0 {
		proc := p.proc
		go func() {
			for _, r := range replies {
				proc.Write(r)
			}
		}()
	}
}

// exited ends the pane as its shell exits.
func (p *Pane) exited() {
	proc := p.proc
	select {
	case <-proc.Exited():
	case <-time.After(time.Second):
		proc.Kill()
		<-proc.Exited()
	}
	code := proc.Code()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.recs.DropLive()
	p.info.ExitCode = &code
	p.info.Process, p.info.Agent, p.info.Runtime = "", "", ""
	p.setStatus(Exited)
	if !p.viewed() {
		p.info.Unread = true
	}
	p.changedLocked()
	p.mu.Unlock()
	p.reg.paneExited(p.info.ID, code)
}

// onBell counts a program's bell, which the app answers as the settings
// say; bells closer than half a second count once.
func (p *Pane) onBell() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.lastBell) < 500*time.Millisecond {
		return
	}
	p.lastBell = now
	p.info.Bells++
	p.changedLocked()
}

func (p *Pane) onTitle(title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.Title != title {
		p.info.Title = title
		p.looked = false
		p.changedLocked()
	}
}

// onNotify takes a program's desktop notification (OSC 9, 777), which
// tells nothing of its status: an agent's screen tells whether it waits,
// and its notification only words the wait. Any other program's goes on
// to the app.
func (p *Pane) onNotify(title, body string) {
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if body == "" {
		title, body = "", title
	}
	if body == "" {
		return
	}
	p.mu.Lock()
	if detect.Known(p.info.Agent) {
		if p.info.Status == Waiting {
			p.info.Message = body
			p.changedLocked()
		}
		p.mu.Unlock()
		return
	}
	id := p.info.ID
	p.mu.Unlock()
	p.reg.notice(id, title, body)
}

// input notes that the user acted in the pane: a wait they answered is
// over, unless the screen still asks; finished reports are dismissed.
func (p *Pane) input() {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	if p.recs.Len() > 0 && p.recs.DismissFinished() {
		p.fromRecords()
		changed = true
	}
	if p.info.Status == Waiting && !p.recs.Declared() && p.seen != detect.Blocked || p.info.Status == Failed {
		p.setStatus(Idle)
		p.info.Message = ""
		changed = true
	}
	if p.info.Unread {
		p.info.Unread = false
		changed = true
	}
	if changed {
		p.changedLocked()
	}
}

// mouse tells mouse reports, SGR's and X10's, from what the user types.
var mouse = regexp.MustCompile(`^\x1b\[(<[0-9;]*[Mm]|M)`)

// typing notes what the user typed, for the mailbox (Pane.typed).
func (p *Pane) typing(b []byte) {
	if len(b) == 0 || reply.Match(b) || mouse.Match(b) {
		return
	}
	p.mu.Lock()
	p.typed, p.typedEnd = time.Now(), endsInput(b)
	p.mu.Unlock()
}

// inputEnd is a key that leaves no draft behind, in the Kitty keyboard
// protocol: Enter (CSI 13 u), Escape (27) and Ctrl+C (99;5).
var inputEnd = regexp.MustCompile(`\x1b\[(13|27|99;5)[;:0-9]*u$`)

// endsInput tells input after which no draft is left: Enter, Ctrl+C,
// Ctrl+U or Escape last.
func endsInput(b []byte) bool {
	switch b[len(b)-1] {
	case '\r', '\n', 0x03, 0x15:
		return true
	case 0x1b:
		return len(b) == 1
	}
	return inputEnd.Match(b)
}

func (p *Pane) viewed() bool { return p.reg.isViewed(p.info.Session) }

// needsUser marks the pane waiting for the user; p.mu is held.
func (p *Pane) needsUser(msg string) {
	if p.info.Status == Exited || p.recs.Declared() {
		return
	}
	p.setStatus(Waiting)
	if msg != "" {
		p.info.Message = msg
	}
	if !p.viewed() {
		p.info.Unread = true
	}
	p.changedLocked()
}

// setStatus changes the status; p.mu is held.
func (p *Pane) setStatus(st Status) {
	if p.info.Status == st {
		return
	}
	prev := p.info.Status
	p.info.Status = st
	p.info.Since = time.Now().UnixMilli()
	p.reg.statusChanged(p, prev, st)
}

// --- OSC ------------------------------------------------------------------

// handleOSC acts on an OSC the scanner found, returning what to answer the
// program, if anything; p.mu is held.
func (p *Pane) handleOSC(body []byte) []byte {
	switch {
	case bytes.HasPrefix(body, []byte("7501;")):
		rep, ok := osc.ParseStatus(string(body[5:]))
		if !ok {
			return nil
		}
		if rep.Query {
			return []byte(osc.QueryReply)
		}
		p.recs.Apply(rep.Record)
		p.reported = true
		p.fromRecords()
		p.changedLocked()
	case bytes.HasPrefix(body, []byte("9;4;")):
		// ConEmu's progress, which only agents' rules read: a program's
		// progress bar, as Neovim's for its language servers, tells no
		// work the user waits on.
		p.progress = string(body[2:])
	case bytes.HasPrefix(body, []byte("133;A")):
		// A new prompt: what ran is over.
		if p.recs.DropLive() {
			p.fromRecords()
			p.changedLocked()
		}
	case bytes.HasPrefix(body, []byte("133;D")):
		if !p.awaiting {
			return nil
		}
		p.awaiting = false
		code, _, _ := strings.Cut(strings.TrimPrefix(string(body[5:]), ";"), ";")
		n, _ := strconv.Atoi(code)
		p.info.CommandExit = &n
		// The command it started with failed (a dev server crashed): the
		// pane says so, which notifies as a failure does. Stopped with
		// Ctrl+C (130) or a SIGTERM (143), it didn't fail.
		if n != 0 && n != 130 && n != 143 && len(p.info.Command) > 0 && p.info.Agent == "" {
			p.info.Message = filepath.Base(p.info.Command[0]) + " exited (code " + strconv.Itoa(n) + ")"
			p.setStatus(Failed)
		}
		p.changedLocked()
	}
	return nil
}

// fullReset forgets every report and the progress, as RIS does; p.mu is
// held.
func (p *Pane) fullReset() {
	if p.recs.Len() > 0 {
		p.recs.Reset()
		p.fromRecords()
	}
	p.reported = false
	p.progress = ""
	p.info.Summary = ""
}

// summarize keeps a report's message as the agent's last word on its
// turn, when it has one; p.mu is held.
func (p *Pane) summarize(msg string) {
	if msg != "" {
		p.info.Summary = msg
	}
}

// worded tells that the lead report says something of itself: the screen
// has no better word on the turn then. p.mu is held.
func (p *Pane) worded() bool {
	lead, ok := p.recs.Lead()
	return ok && lead.Msg != ""
}

// fromRecords sets the status from the program's reports, which come
// before what the screen would tell; p.mu is held.
func (p *Pane) fromRecords() {
	p.info.Records = p.recs.List()
	lead, ok := p.recs.Lead()
	if !ok {
		p.info.Kind, p.info.Progress = "", -1
		if p.info.Status == Failed || p.reported && (p.info.Status == Working || p.info.Status == Waiting) {
			p.setStatus(Idle)
			p.info.Message = ""
		}
		return
	}
	viewed := p.viewed()
	switch lead.State {
	case "blocked":
		p.setStatus(Waiting)
		p.info.Message, p.info.Kind, p.info.Progress = lead.Msg, lead.Kind, lead.Progress
		p.summarize(lead.Msg)
		if !viewed {
			p.info.Unread = true
		}
	case "working":
		p.setStatus(Working)
		p.info.Message, p.info.Kind, p.info.Progress = lead.Msg, "", lead.Progress
		p.info.Summary = "" // a turn: its last word is to come
	case "error":
		p.setStatus(Failed)
		p.info.Message, p.info.Kind, p.info.Progress = lead.Msg, "", -1
		p.summarize(lead.Msg)
		if !viewed {
			p.info.Unread = true
		}
	case "done":
		if p.info.Status != Idle || p.info.Message != lead.Msg {
			p.setStatus(Idle)
			if !viewed {
				p.info.Unread = true
			}
		}
		p.info.Message, p.info.Kind, p.info.Progress = lead.Msg, "", -1
		p.summarize(lead.Msg)
	default:
		p.setStatus(Idle)
		p.info.Message, p.info.Kind, p.info.Progress = "", "", -1
	}
}

// --- the tick ---------------------------------------------------------------

// procName, procArgs and procCwd read a process; tests replace them.
var (
	procName = pty.ProcName
	procArgs = pty.ProcArgs
	procCwd  = pty.ProcCwd
)

// tick follows the foreground program and the shell's directory, and reads
// the screen of a pane that changed. It reports whether Info changed.
func (p *Pane) tick(now time.Time) bool {
	p.mu.Lock()
	if p.closed || p.proc == nil || p.info.Status == Exited {
		p.mu.Unlock()
		return false
	}
	pg := p.proc.Foreground()
	shellPID := p.info.PID
	agentBefore := p.info.Agent
	needScreen := !p.looked || !p.idleFrom.IsZero()
	p.mu.Unlock()

	proc, ag, runtime := "", "", ""
	if pg != 0 && pg != shellPID {
		proc = procName(pg)
		args := procArgs(pg)
		ag = agent.Detect(proc, args)
		if proc == "node" || proc == "bun" || proc == "python" || proc == "python3" || proc == "deno" {
			// A script: name it by the script, as "codex", not "node".
			if len(args) > 1 {
				runtime = strings.TrimSuffix(proc, "3")
				proc = strings.TrimSuffix(filepath.Base(args[1]), filepath.Ext(args[1]))
			}
		}
		if ag != "" {
			proc = ag
		}
	}
	// The screen, read before taking p.mu: the terminal has a lock of its
	// own, never to be taken under ours.
	screen := ""
	if needScreen && detect.Known(ag) {
		screen = p.screen()
	}
	var cwd, pcwd string
	readCwd := false
	p.mu.Lock()
	if now.Sub(p.cwdAt) > 1500*time.Millisecond || proc == "" && p.info.ProcessDir != "" {
		readCwd = true
	}
	p.mu.Unlock()
	if readCwd {
		if p.term != nil {
			cwd = p.term.Dir()
		}
		if cwd == "" {
			cwd = procCwd(shellPID)
		}
		if proc != "" {
			pcwd = procCwd(pg)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	changed := false
	p.info.FgPGID = pg
	if proc == "" && p.info.Process != "" {
		// The program ended: its live reports go with it, and an agent's
		// conversation is no more to resume.
		if p.recs.DropLive() {
			p.fromRecords()
		}
		p.progress = ""
		p.info.Summary = ""
		if p.info.AgentSession != nil {
			p.info.AgentSession = nil
		}
		changed = true
	}
	if proc != p.info.Process || ag != p.info.Agent || runtime != p.info.Runtime {
		if agentBefore != "" && ag != agentBefore {
			p.progress = ""
		}
		p.info.Process, p.info.Agent, p.info.Runtime = proc, ag, runtime
		p.seen, p.looked, p.prompt = "", false, false
		changed = true
	}
	if readCwd {
		p.cwdAt = now
		if cwd != "" && cwd != p.info.Dir {
			p.info.Dir = cwd
			changed = true
		}
		if pcwd == p.info.Dir {
			pcwd = ""
		}
		if pcwd != p.info.ProcessDir {
			p.info.ProcessDir = pcwd
			changed = true
		}
	}
	if p.look(now, screen) {
		changed = true
	}
	if changed {
		p.changedLocked()
	}
	return changed
}

// screen returns the text of the screen: the last rows of the terminal's
// text.
func (p *Pane) screen() string {
	if p.term == nil {
		return ""
	}
	text := strings.TrimRight(p.term.Text(), "\n")
	p.mu.Lock()
	rows := p.rows
	p.mu.Unlock()
	if rows <= 0 {
		rows = 40
	}
	lines := strings.Split(text, "\n")
	return strings.Join(lines[max(0, len(lines)-rows):], "\n") + "\n"
}

// look sets the status from what the screen shows, when it may have
// changed: an agent's from its rules, any other program's from its title,
// which turns as a spinner while it works. Only a change of what shows
// changes the status, so that a redraw changes nothing; p.mu is held.
func (p *Pane) look(now time.Time, screen string) bool {
	if p.recs.Declared() {
		// What the program declares stands; once it ends, the screen
		// tells again.
		p.seen = ""
		return false
	}
	if p.looked && p.idleFrom.IsZero() || p.info.Status == Exited {
		return false
	}
	p.looked = true
	st, visible := detect.Idle, true
	changed := false
	if detect.Known(p.info.Agent) {
		r := detect.Detect(p.info.Agent, detect.Input{Screen: screen, Title: p.info.Title, Progress: p.progress})
		p.prompt = r.State == detect.Idle && r.Visible && !r.Skip
		// At its prompt, the agent's last word on its turn is what its
		// screen ended on, unless its reports gave one.
		if p.prompt && !p.worded() {
			if s := detect.Summary(p.info.Agent, screen); s != "" && s != p.info.Summary {
				p.info.Summary, changed = s, true
			}
		}
		if r.Skip {
			return changed
		}
		st, visible = r.State, r.Visible
	} else if agent.TitleBusy(p.info.Title) {
		st = detect.Working
	}
	if st == p.seen {
		p.idleFrom = time.Time{}
		return changed
	}
	if st == detect.Idle && !visible && p.info.Status == Working {
		if p.idleFrom.IsZero() {
			p.idleFrom = now
		}
		if now.Sub(p.idleFrom) < idleHold {
			return false
		}
	}
	p.idleFrom = time.Time{}
	p.seen = st
	before := p.info.Status
	switch st {
	case detect.Working:
		p.setStatus(Working)
		p.info.Message, p.info.Summary = "", ""
	case detect.Blocked:
		p.needsUser("")
	default:
		if before == Working || before == Waiting {
			p.setStatus(Idle)
			p.info.Message = ""
			if before == Working && !p.viewed() {
				p.info.Unread = true
			}
		}
	}
	return p.info.Status != before || changed
}

// commandLine is the argv a pane starts: the user's login shell, which
// runs Command first when set; or Command itself (Exec).
func commandLine(o StartOptions) (path string, args []string, shell string) {
	if o.Exec && len(o.Command) > 0 {
		return lookPath(o.Command[0]), o.Command, ""
	}
	shell = o.Shell
	if shell == "" {
		shell = shellcmd.Login()
	}
	return shell, shellcmd.Args(shell, o.Command), shell
}

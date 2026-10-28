// Partly adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/proto/proto.go.

// Package proto is what goes over omaterm's control socket: a hello and
// its welcome, then requests and their answers, one JSON object a line,
// and events to subscribers. The app serves it, the omaterm command and
// agents' hooks call it, and a session daemon will serve it the same.
package proto

import (
	"bufio"
	"encoding/json"
	"errors"

	"omaterm/internal/model"
	"omaterm/internal/session"
)

// Version is the protocol's; a client of another is turned away.
const Version = 1

// Hello is a client's first line.
type Hello struct {
	Kind    string `json:"kind"` // "control", or "attach" (a pane's screen)
	Version int    `json:"version"`
	Client  string `json:"client,omitempty"`
	// Pane is the pane an attach connection shows.
	Pane string `json:"pane,omitempty"`
}

// Welcome is the server's answer to Hello.
type Welcome struct {
	Version int    `json:"version"`
	PID     int    `json:"pid"`
	Started int64  `json:"started"`
	App     string `json:"app,omitempty"`
	Error   string `json:"error,omitempty"`
	// Cols and Rows are an attached pane's size.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// Request is a call: a method and its params.
type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Message is an answer (ID set) or an event (Event set).
type Message struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
	Event  string          `json:"event,omitempty"`
	State  *State          `json:"state,omitempty"`
}

// Error codes, which the CLI maps to its exit statuses.
const (
	CodeUsage         = "usage"
	CodeNotFound      = "not_found"
	CodeRemoteControl = "remote_control"
	CodeBusy          = "busy"
	CodeTimeout       = "timeout"
	// CodeAppClosed is a call only the window can answer, made while it
	// is closed: the session daemon answers the rest.
	CodeAppClosed = "app_closed"
	// CodeInternal is a call that failed inside omaterm: a bug, logged.
	CodeInternal = "internal"
)

// Error is a call's error with a code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Errorf makes an Error.
func Errorf(code, msg string) error { return &Error{Code: code, Msg: msg} }

// CodeOf is an error's code, "" for none.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Pane is a pane, as the app knows it.
type Pane = session.Info

// Session is a terminal tab: its panes, laid out, and the pane that
// speaks for it (Lead).
type Session struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	// AgentNamed tells a name an agent gave the session, which the next
	// conversation there may change.
	AgentNamed bool          `json:"agentNamed,omitempty"`
	Focus      string        `json:"focus,omitempty"`
	Zoomed     string        `json:"zoomed,omitempty"`
	Layout     *model.Layout `json:"layout,omitempty"`
	Panes      []Pane        `json:"panes"`
	Lead       Pane          `json:"lead"`
	// Worktree is the git worktree the session works in.
	Worktree *model.Worktree `json:"worktree,omitempty"`
	// Parent is the session whose agent started this one.
	Parent string `json:"parent,omitempty"`
}

// DropGoneParents forgets the parents that are no more: a session whose
// parent closed stands on its own.
func DropGoneParents(sessions []Session) {
	ids := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		ids[s.ID] = true
	}
	for i := range sessions {
		if !ids[sessions[i].Parent] {
			sessions[i].Parent = ""
		}
	}
}

// Project is a folder the user works in.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// State is everything: projects, their sessions, and the session the
// window shows.
type State struct {
	Projects []Project `json:"projects"`
	Sessions []Session `json:"sessions"`
	Viewed   string    `json:"viewed,omitempty"`
	Started  int64     `json:"started"`
}

// CreateParams make a session (create) or a pane beside one (split).
type CreateParams struct {
	Project string   `json:"project,omitempty"` // id or name; else by Dir, else the shown one
	Name    string   `json:"name,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Command []string `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"`
	Focus   bool     `json:"focus,omitempty"`
	URL     string   `json:"url,omitempty"`
	// Split is the pane (or session) to split; Direction right, down,
	// left or up; Ratio what the new pane takes (0..1).
	Split     string  `json:"split,omitempty"`
	Direction string  `json:"direction,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
	// Worktree makes the session a git worktree of its own to work in.
	Worktree *WorktreeParams `json:"worktree,omitempty"`
	// By is the pane of the agent making the session: its session is the
	// new one's parent.
	By string `json:"by,omitempty"`
}

// WorktreeParams make a session's worktree: on Branch, a new branch from
// Base (the checkout's own when ""), or one not checked out elsewhere.
type WorktreeParams struct {
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
	// NoEnv leaves out the checkout's .env files, copied otherwise.
	NoEnv bool `json:"noEnv,omitempty"`
}

// Created is what create and split made.
type Created struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
}

type IDParams struct {
	ID string `json:"id"`
}

type RenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	By   string `json:"by,omitempty"` // the pane of an agent naming its session
}

type SendParams struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

type ReadParams struct {
	ID         string `json:"id"`
	Scrollback bool   `json:"scrollback,omitempty"`
	Lines      int    `json:"lines,omitempty"`
}

type ReadResult struct {
	Text string `json:"text"`
}

// WaitParams wait for a pane: until its status is one of Status, or, with
// none, until it stops working; with Exit, until its command ends.
type WaitParams struct {
	ID        string   `json:"id"`
	Status    []string `json:"status,omitempty"`
	Exit      bool     `json:"exit,omitempty"`
	TimeoutMS int64    `json:"timeout,omitempty"`
}

// Mail is a message between agents, or from omaterm to one, as the
// mailbox carries it (package mail).
type Mail struct {
	ID string `json:"id"`
	// From is the asking pane, "" for omaterm itself or a command run
	// outside it; To the pane whose agent the message is for.
	From     string `json:"from,omitempty"`
	FromName string `json:"fromName,omitempty"`
	To       string `json:"to"`
	ToName   string `json:"toName,omitempty"`
	Text     string `json:"text"`
	// Note wants no answer.
	Note  bool   `json:"note,omitempty"`
	State string `json:"state"`
	Reply string `json:"reply,omitempty"`
	// Screen is the end of the agent's screen when it stopped without
	// replying; Why why the message waits, or failed.
	Screen  string `json:"screen,omitempty"`
	Why     string `json:"why,omitempty"`
	Created int64  `json:"created"`
}

// A Mail's states.
const (
	MailQueued    = "queued"    // waiting for its agent to be free
	MailDelivered = "delivered" // typed into its agent's prompt
	MailReplied   = "replied"
	MailNoReply   = "noreply" // its agent stopped without replying
	MailFailed    = "failed"  // it couldn't be delivered
)

// MailAskParams send a message (mail.ask). Its answer comes to whoever
// waits for it (mail.await), else, with NoWait or once none waits, to
// the asker as a message of its own.
type MailAskParams struct {
	From     string `json:"from,omitempty"`
	FromName string `json:"fromName,omitempty"`
	To       string `json:"to"`
	ToName   string `json:"toName,omitempty"`
	Text     string `json:"text"`
	Note     bool   `json:"note,omitempty"`
	NoWait   bool   `json:"noWait,omitempty"`
}

// MailAwaitParams wait for a message's answer (mail.await).
type MailAwaitParams struct {
	ID        string `json:"id"`
	TimeoutMS int64  `json:"timeout,omitempty"`
}

// MailReplyParams answer a message (mail.reply): ID, else the last one
// From's agent was asked.
type MailReplyParams struct {
	ID   string `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	Text string `json:"text"`
}

// MailLimitParams let pane From ask pane To at most N times more
// (mail.limit), as a review's rounds.
type MailLimitParams struct {
	From string `json:"from"`
	To   string `json:"to"`
	N    int    `json:"n"`
}

// MailListParams list the messages to and from a pane (mail.list); all
// of them for none.
type MailListParams struct {
	Pane string `json:"pane,omitempty"`
}

// MergeParams merge the worktree of session Target into the branch of
// pane By's session (worktree.merge): squashed, unless Commit; its
// worktree and session removed after, unless Keep.
type MergeParams struct {
	By      string `json:"by"`
	Target  string `json:"target"`
	Commit  bool   `json:"commit,omitempty"`
	Message string `json:"message,omitempty"`
	Keep    bool   `json:"keep,omitempty"`
}

// Merged is what worktree.merge did.
type Merged struct {
	Branch  string   `json:"branch"`
	Into    string   `json:"into"`
	Commits []string `json:"commits,omitempty"`
	Removed bool     `json:"removed"`
}

// ReportParams give pane ID a status report (report), as OSC 7501 would:
// Body is what follows "7501;", for a program with no terminal to write
// it to, as an agent's commands.
type ReportParams struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

type NotifyParams struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// AgentSessionParams tell the pane an agent runs in its own session id,
// from the agent's SessionStart hook.
type AgentSessionParams struct {
	PID     int    `json:"pid"`
	Agent   string `json:"agent"`
	Session string `json:"session"`
	Dir     string `json:"dir,omitempty"`
}

type AgentSessionResult struct {
	Pane    string `json:"pane"`
	Session string `json:"session"`
	// Away tells an agent whose commands run away from its pane, without
	// its variables, as OpenCode's service's.
	Away bool `json:"away,omitempty"`
}

type ProjectParams struct {
	Name string `json:"name,omitempty"`
	Dir  string `json:"dir"`
}

type DoParams struct {
	Action string `json:"action"`
}

// MaxLine bounds a line: the whole state, a screenshot.
const MaxLine = 64 << 20

// ReadLine reads a line of at most max bytes.
func ReadLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			return nil, errors.New("proto: line too long")
		}
		line = append(line, chunk...)
		if err == nil {
			return line, nil
		}
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

// GitParams ask for a pane's changes against a base: "turn" (where its
// agent's last turn started), "head" (uncommitted) or "branch" (since
// the default branch), of some paths or all.
type GitParams struct {
	Pane  string   `json:"pane"`
	Base  string   `json:"base,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// BrowserParams are what `omaterm browser` asks of a browser pane: an op,
// and the element (a CSS selector), text and timeout it takes.
type BrowserParams struct {
	ID        string `json:"id"`
	Op        string `json:"op"`
	Selector  string `json:"selector,omitempty"`
	Text      string `json:"text,omitempty"`
	Enter     bool   `json:"enter,omitempty"`
	TimeoutMS int64  `json:"timeout,omitempty"`
}

// BrowserResult is what it answers: text, or a screenshot.
type BrowserResult struct {
	Text string `json:"text,omitempty"`
	PNG  []byte `json:"png,omitempty"`
}

// Clients are who the session daemon serves: the apps following it, and
// the clients showing each pane.
type Clients struct {
	Apps     int            `json:"apps"`
	Attached map[string]int `json:"attached"`
}

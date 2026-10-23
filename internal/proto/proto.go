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
	Kind    string `json:"kind"` // "control"
	Version int    `json:"version"`
	Client  string `json:"client,omitempty"`
}

// Welcome is the server's answer to Hello.
type Welcome struct {
	Version int    `json:"version"`
	PID     int    `json:"pid"`
	Started int64  `json:"started"`
	App     string `json:"app,omitempty"`
	Error   string `json:"error,omitempty"`
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

// Package session runs the app's panes: a shell in a pseudo-terminal the
// app owns, its terminal emulator, and what the app knows of it — the
// program in its foreground, its directory, and its status (working,
// waiting for the user, idle) from the status reports programs send and
// from what agents show on screen.
//
// The status engine is a port of Kero's (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE), internal/daemon/session.go and osc.go.
//
// It knows nothing of windows: the app shows a Pane's Term, the CLI writes
// to it and reads it, and both learn of changes through Registry.Changed.
package session

import (
	"omaterm/internal/agent"
	"omaterm/internal/osc"
)

// Status is what a pane's program is doing, as the sidebar shows it.
type Status string

const (
	Idle    Status = "idle"
	Working Status = "working"
	Waiting Status = "waiting" // needs the user: a permission, a question
	Failed  Status = "error"
	Exited  Status = "exited"
)

// Turn is where an agent's last turn started: a snapshot of its worktree
// (a git tree), which the turn's changes are taken against.
type Turn struct {
	Tree string `json:"tree"`
	Root string `json:"root"`
	At   int64  `json:"at"` // unix ms
}

// Info is what is known of a pane at a moment.
type Info struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Project string `json:"project,omitempty"`

	PID    int `json:"pid,omitempty"`
	FgPGID int `json:"fgpgid,omitempty"`
	// Process is the foreground program, "" while the shell waits at its
	// prompt; Agent the coding agent it is, if any; Runtime the
	// interpreter of a script (node, bun, python), named by its script.
	Process string `json:"process,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Runtime string `json:"runtime,omitempty"`
	Shell   string `json:"shell,omitempty"`

	// Dir is the shell's directory; ProcessDir the foreground program's
	// when it differs, as an agent's that went into a worktree.
	Dir        string `json:"dir,omitempty"`
	ProcessDir string `json:"processDir,omitempty"`
	Title      string `json:"title,omitempty"`
	// URL is a browser pane's page; a browser pane runs no shell.
	URL     string   `json:"url,omitempty"`
	Command []string `json:"command,omitempty"`

	Status Status `json:"status"`
	// Since is when Status last changed, Activity when output last came
	// (unix ms).
	Since    int64 `json:"since"`
	Activity int64 `json:"activity"`
	// Unread marks a change the user has not seen: their session was not
	// in view when it came.
	Unread bool `json:"unread,omitempty"`
	// Message words the status, as a waiting agent's question; Kind is a
	// waiting report's kind (permission, question, auth).
	Message  string       `json:"message,omitempty"`
	Kind     string       `json:"kind,omitempty"`
	Progress int          `json:"progress"` // -1 when none
	Records  []osc.Record `json:"records,omitempty"`

	// Bells counts the program's bells (BEL).
	Bells int `json:"bells,omitempty"`

	ExitCode *int `json:"exitCode,omitempty"`
	// CommandExit is the exit status of the command the pane started
	// with, once it ended (OSC 133;D).
	CommandExit *int `json:"commandExit,omitempty"`

	AgentSession *agent.Session `json:"agentSession,omitempty"`
	Turn         *Turn          `json:"turn,omitempty"`
	Restored     bool           `json:"restored,omitempty"`
}

// WorkDir is the directory the pane works in: its program's, else its
// shell's.
func (i Info) WorkDir() string {
	if i.ProcessDir != "" {
		return i.ProcessDir
	}
	return i.Dir
}

// urgency orders panes by how much they need the user.
func urgency(i Info) int {
	switch {
	case i.Status == Waiting:
		return 5
	case i.Status == Failed:
		return 4
	case i.Status == Exited && i.Unread:
		return 3
	case i.Unread:
		return 2
	case i.Status == Working:
		return 1
	}
	return 0
}

// Lead is the pane of a session that speaks for it: the one that needs the
// user most; the first on a tie. ok is false for none.
func Lead(infos []Info) (Info, bool) {
	best, ok := Info{}, false
	for _, i := range infos {
		if !ok || urgency(i) > urgency(best) {
			best, ok = i, true
		}
	}
	return best, ok
}

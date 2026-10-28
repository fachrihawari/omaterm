// Package model is what the app keeps of its projects and sessions between
// runs: state.json. A session is a terminal tab, whose panes are laid out
// as a binary tree of splits.
//
// The schema is the app's and, later, the session daemon's: it holds no UI
// state (sizes of the sidebar and inspector live in ui.json), so whoever
// owns the sessions owns this file.
package model

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/session"
)

// Version is the schema's version; version 1 was the project list alone.
const Version = 2

// State is state.json.
type State struct {
	Version       int       `json:"version"`
	Saved         int64     `json:"saved"`
	ActiveProject string    `json:"activeProject,omitempty"`
	Projects      []Project `json:"projects"`
	Sessions      []Session `json:"sessions"`
}

// Project is a folder the user works in.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Collapsed bool   `json:"collapsed,omitempty"`
	// ActiveSession is the session it showed last.
	ActiveSession string `json:"activeSession,omitempty"`
}

// Session is a terminal tab of a project.
type Session struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Name is what the user (or an agent, AgentNamed) called it; "" names
	// it after what it runs.
	Name       string  `json:"name,omitempty"`
	AgentNamed bool    `json:"agentNamed,omitempty"`
	Created    int64   `json:"created,omitempty"`
	Focus      string  `json:"focus,omitempty"`
	Zoomed     string  `json:"zoomed,omitempty"`
	Layout     *Layout `json:"layout,omitempty"`
	Panes      []Pane  `json:"panes"`
	// Worktree is the git worktree the session works in, nil for none.
	Worktree *Worktree `json:"worktree,omitempty"`
}

// Worktree is a git worktree omaterm made for a session (internal/worktree):
// its folder, its branch, and the branch it merges into.
type Worktree struct {
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
}

// Pane is a pane of a session: a shell, or a browser page (URL).
type Pane struct {
	ID      string         `json:"id"`
	Dir     string         `json:"dir,omitempty"`
	Command []string       `json:"command,omitempty"`
	URL     string         `json:"url,omitempty"`
	Title   string         `json:"title,omitempty"`
	Agent   *agent.Session `json:"agent,omitempty"`
	Turn    *session.Turn  `json:"turn,omitempty"`
	Cols    int            `json:"cols,omitempty"`
	Rows    int            `json:"rows,omitempty"`
	Created int64          `json:"created,omitempty"`
}

// Layout is a session's panes: a leaf (Pane), or a split of A and B, side
// by side when Row, A taking Ratio (0..1) of the room.
type Layout struct {
	Pane  string  `json:"pane,omitempty"`
	Row   bool    `json:"row,omitempty"`
	Ratio float64 `json:"ratio,omitempty"`
	A     *Layout `json:"a,omitempty"`
	B     *Layout `json:"b,omitempty"`
}

// Panes returns the layout's panes, left to right and top to bottom.
func (l *Layout) Panes() []string {
	if l == nil {
		return nil
	}
	if l.Pane != "" {
		return []string{l.Pane}
	}
	return append(l.A.Panes(), l.B.Panes()...)
}

// v1 is the state.json of before sessions: projects and their tab names.
type v1 struct {
	Projects []struct {
		Name string   `json:"Name"`
		Path string   `json:"Path"`
		Tabs []string `json:"Tabs"`
	} `json:"projects"`
	Active int `json:"active"`
}

// NewID returns a short random id for a project, session or pane: short
// enough to type as a CLI target, from an alphabet without look-alikes.
var NewID = RandomID

// RandomID is NewID's default.
func RandomID() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	var b [7]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}

// Load reads a state file, migrating a version 1 one: each project gets an
// id, and each of its tab names becomes a session with one pane.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return State{}, err
	}
	if probe.Version >= 2 {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return State{}, err
		}
		return st, nil
	}
	var old v1
	if err := json.Unmarshal(data, &old); err != nil {
		return State{}, err
	}
	st := State{Version: Version}
	for i, p := range old.Projects {
		proj := Project{ID: NewID(), Name: p.Name, Dir: p.Path}
		st.Projects = append(st.Projects, proj)
		if i == old.Active {
			st.ActiveProject = proj.ID
		}
		for _, name := range p.Tabs {
			pane := NewID()
			st.Sessions = append(st.Sessions, Session{
				ID: NewID(), Project: proj.ID, Name: name, Focus: pane,
				Layout: &Layout{Pane: pane}, Panes: []Pane{{ID: pane, Dir: p.Path}},
			})
		}
	}
	return st, nil
}

// ErrNewer is Save's error for a file a newer omaterm wrote, in a form
// this one doesn't know: written over, what only the newer one keeps was
// lost.
var ErrNewer = errors.New("the sessions file is from a newer omaterm")

// Save writes a state file atomically, readable by its owner only.
func Save(path string, st State) error {
	if data, err := os.ReadFile(path); err == nil {
		var probe struct {
			Version int `json:"version"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Version > Version {
			return ErrNewer
		}
	}
	st.Version = Version
	st.Saved = time.Now().UnixMilli()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A temporary file of its own, so that the app and the session daemon
	// never write the same one, synced before it replaces the file, so a
	// crash leaves the old state or the new, never half of one.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Unreadable tells a state file that is there but can't be read, as one
// a crash or an editor left broken, from one that isn't there.
func Unreadable(err error) bool {
	return err != nil && !errors.Is(err, fs.ErrNotExist)
}

// Backup copies a state file that can't be read beside it, named for the
// time, and returns the copy's path.
func Backup(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	bad := path + ".bad-" + time.Now().Format("20060102-150405")
	return bad, os.WriteFile(bad, data, 0o600)
}

// ErrNoState is Load's error for a missing file.
var ErrNoState = errors.New("model: no state")

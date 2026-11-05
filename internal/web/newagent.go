package web

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/gitsnap"
	"omaterm/internal/proto"
	"omaterm/internal/worktree"
)

// New Agent from the phone: a session in a project, running one of the
// agents the window's dialog offers, in a worktree of its own or not, its
// agent given a first task. The page names the agent, never a command:
// the daemon types a session's command into a login shell, so anything
// else would be the page's own shell line.

// The new session's limits: what the page sends whole, a first task, a
// branch's and a project's name.
const (
	maxCreateBody = 32 << 10
	maxTask       = 16 << 10
	maxBranch     = 100
	maxProject    = 200
)

// createWait is how long create waits for a slow worktree before it
// answers that the session is on its way: a tunnel cuts a request short
// (Cloudflare's at 100 seconds), and git goes on all the same.
var createWait = 40 * time.Second

// createLimit bounds a new session's making and its first task's queueing,
// once the page no longer waits: the daemon's own worktree timeout, and
// some.
const createLimit = 6 * time.Minute

// newSession is what the page asks of a new session: its project; its
// agent, of agent.Starters ("" or "shell" for a shell); a worktree of its
// own, on a new branch from the project's; and a first task.
type newSession struct {
	Project  string `json:"project"`
	Agent    string `json:"agent,omitempty"`
	Worktree *struct {
		Branch string `json:"branch"`
	} `json:"worktree,omitempty"`
	Task string `json:"task,omitempty"`
}

// params checks what the page asked for, and makes it the daemon's call.
func (n newSession) params() (proto.CreateParams, error) {
	p := proto.CreateParams{Project: n.Project}
	task := strings.TrimSpace(n.Task)
	switch {
	case n.Agent == "" || n.Agent == "shell":
		if task != "" {
			return p, errors.New("a shell takes no first task: pick an agent")
		}
	case slices.Contains(agent.Starters, n.Agent):
		p.Command = []string{n.Agent}
	default:
		return p, errors.New("unknown agent")
	}
	if len(n.Task) > maxTask {
		return p, errors.New("the first task is too long: put the rest in a file, and name it")
	}
	if n.Worktree != nil {
		b := strings.TrimSpace(n.Worktree.Branch)
		// ValidBranch refuses a leading '-' too; git would read it as an
		// option.
		if len(b) > maxBranch || strings.HasPrefix(b, "-") {
			return p, errors.New("a branch can't be named so")
		}
		if err := worktree.ValidBranch(b); err != nil {
			return p, err
		}
		p.Worktree = &proto.WorktreeParams{Branch: b}
	}
	return p, nil
}

// create makes a session, as the page's New Agent sheet asks, and gives
// its agent the first task once it is up. A worktree slower than
// createWait is answered 202 with its branch: the session shows in the
// state once it is made.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var n newSession
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateBody))
	// A command, or anything else unknown, is refused, not ignored.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&n); err != nil || n.Project == "" || len(n.Project) > maxProject {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, err := n.params()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params, _ := json.Marshal(p)
	type made struct {
		created proto.Created
		err     error
		taskErr error
	}
	done := make(chan made, 1)
	go func() {
		// The page going away, or a tunnel cutting it short, stops neither
		// git midway nor the task.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), createLimit)
		defer cancel()
		var m made
		v, err := s.d.Handle(ctx, "create", params)
		if err == nil {
			raw, _ := json.Marshal(v)
			if err = json.Unmarshal(raw, &m.created); err == nil && m.created.Pane == "" {
				err = errors.New("no pane was made")
			}
		}
		m.err = err
		if err == nil && strings.TrimSpace(n.Task) != "" {
			m.taskErr = s.brief(ctx, m.created.Pane, n.Task)
		}
		if m.err != nil || m.taskErr != nil {
			log.Printf("web: new session in %s: %v, task: %v", n.Project, m.err, m.taskErr)
		}
		done <- m
	}()
	// Only git takes long: a session without a worktree is answered as made.
	var slow <-chan time.Time
	if p.Worktree != nil {
		slow = time.After(createWait)
	}
	select {
	case <-r.Context().Done():
	case m := <-done:
		if m.err != nil {
			http.Error(w, m.err.Error(), http.StatusBadGateway)
			return
		}
		out := map[string]string{"session": m.created.Session, "pane": m.created.Pane}
		if m.taskErr != nil {
			out["taskError"] = m.taskErr.Error()
		}
		writeJSON(w, out)
	case <-slow:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"branch": p.Worktree.Branch})
	}
}

// briefBoot is how long a first task waits for its agent to start: Codex,
// or a cold Claude Code on a slow laptop, can take longer than the
// mailbox's 30 s. One that doesn't start shows why in its terminal.
const briefBoot = 2 * time.Minute

// brief queues a new agent's first task, typed in once it is at its
// prompt: a note, from omaterm, that wants no answer.
func (s *Server) brief(ctx context.Context, pane, task string) error {
	params, _ := json.Marshal(proto.MailAskParams{To: pane, Text: task, Note: true, BootMS: briefBoot.Milliseconds()})
	_, err := s.d.Handle(ctx, "mail.ask", params)
	return err
}

// starter is an agent the sheet offers. Found tells its command is on
// this process's PATH; one on the login shell's alone starts all the
// same, so the sheet dims the others, and still offers them.
type starter struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Found bool   `json:"found"`
}

// newOptions is what the sheet offers for a new session in the project
// the query names: the agents, and whether it can have a worktree, from
// which branch ("" when detached: the default branch).
func (s *Server) newOptions(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Agents []starter `json:"agents"`
		Git    bool      `json:"git"`
		Branch string    `json:"branch"`
	}{}
	for _, name := range agent.Starters {
		a := starter{Name: name, Label: agent.DisplayName(name), Found: true}
		if name == "shell" {
			a.Label = "Shell only"
		} else if _, err := exec.LookPath(name); err != nil {
			a.Found = false
		}
		out.Agents = append(out.Agents, a)
	}
	if dir := s.projectDir(r.Context(), r.URL.Query().Get("project")); dir != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if repo, err := gitsnap.Open(ctx, dir); err == nil {
			out.Git = true
			b, _ := gitsnap.Command(ctx, "-C", repo.Root, "branch", "--show-current").Output()
			out.Branch = strings.TrimSpace(string(b))
		}
	}
	writeJSON(w, out)
}

// projectDir is the folder of the project id, "" for none.
func (s *Server) projectDir(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	v, err := s.d.Handle(ctx, "state", nil)
	if err != nil {
		return ""
	}
	raw, _ := json.Marshal(v)
	var st proto.State
	if json.Unmarshal(raw, &st) != nil {
		return ""
	}
	for _, p := range st.Projects {
		if p.ID == id {
			return p.Dir
		}
	}
	return ""
}

package main

// Ask for Review: a second agent reviews a session's work, and sends the
// agent there what to fix, round after round (package mail), until
// nothing important is left.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// reviewRounds is how many times a reviewer sends the agent it reviews
// what to fix.
const reviewRounds = 3

// reviewAsk is the Ask for Review dialog.
type reviewAsk struct {
	open    bool
	session string // the session whose work is reviewed
	name    string
	agent   string // the reviewer, of worktreeAgents but the shell
	err     string
}

// reviewers are the agents that review: worktreeAgents', but the shell.
var reviewers = worktreeAgents[:len(worktreeAgents)-1]

// reviewCommand is the command that starts a reviewer; tests replace it.
var reviewCommand = func(agent string) []string { return []string{agent} }

// agentOf is the pane of a session that runs an agent, and what is known
// of it.
func (a *appState) agentOf(t topTab) (string, session.Info, bool) {
	if t.Split == nil {
		return "", session.Info{}, false
	}
	for _, id := range t.Split.termIDs() {
		if i, ok := a.status[id]; ok && i.Agent != "" && i.Status != session.Exited {
			return id, i, true
		}
	}
	return "", session.Info{}, false
}

// activeTabRef is the session shown, nil for none.
func (a *appState) activeTabRef() *topTab {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || a.tabs[a.activeTab].Kind != tabTerminal {
		return nil
	}
	return &a.tabs[a.activeTab]
}

// openReviewDialog asks which agent reviews a session's work: the last
// one chosen, else one other than the session's own, for a second view.
func (a *appState) openReviewDialog(t topTab) {
	_, info, ok := a.agentOf(t)
	if !ok {
		a.toast(nil, "No agent works in "+a.tabName(t)+" to review")
		return
	}
	st := &reviewAsk{open: true, session: t.ID, name: a.tabName(t), agent: a.reviewAgent}
	if !slices.Contains(reviewers, st.agent) {
		st.agent = reviewers[0]
		if info.Agent == st.agent {
			st.agent = reviewers[1]
		}
	}
	a.review = st
}

// reviewDialog is the Ask for Review dialog, while open.
func (a *appState) reviewDialog(c *ui.Context) {
	st := a.review
	if st == nil {
		return
	}
	if !st.open {
		a.review = nil
		return
	}
	start := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		dialogTitle(c, "Ask for Review", false)
		ui.Text(c, fmt.Sprintf("Another agent reads what %s changed, changing nothing itself, and sends it what to fix, "+
			"for up to %d rounds.", st.name, reviewRounds)).FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		dialogField(c, "Reviewer", func() {
			opts := make([][2]string, len(reviewers))
			for i, name := range reviewers {
				opts[i] = [2]string{name, agent.DisplayName(name)}
			}
			settingsPopup(c, "review-agent", &st.agent, opts)
		})
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(pal.Bad).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Cancel", false, false) {
				st.open = false
			}
			if a.dialogButton(c, "Start Review", true, false) {
				start = true
			}
		})
	})
	if start {
		a.startReview(st)
	}
}

// startReview starts the reviewer the dialog asks for: a session under
// the reviewed one, in its folder, given its brief once its agent is up.
func (a *appState) startReview(st *reviewAsk) {
	_, tabs, ti, ok := a.findSession(st.session)
	if !ok {
		st.err = st.name + " closed"
		return
	}
	t := (*tabs)[ti]
	pane, info, ok := a.agentOf(t)
	if !ok {
		st.err = "No agent works in " + st.name + " anymore"
		return
	}
	a.reviewAgent = st.agent
	v, err := a.cliCreate(proto.CreateParams{Name: "review " + st.name, Dir: info.WorkDir(), Command: reviewCommand(st.agent), By: pane})
	if err != nil {
		st.err = "Couldn't start the reviewer: " + err.Error()
		return
	}
	reviewer := v.(proto.Created).Pane
	a.review = nil
	brief := reviewBrief(st.name, pane, t.Worktree != nil)
	a.mailJob(func(ctx context.Context, call func(string, any) error) error {
		if err := call("mail.limit", proto.MailLimitParams{From: reviewer, To: pane, N: reviewRounds}); err != nil {
			return err
		}
		return call("mail.ask", proto.MailAskParams{To: reviewer, ToName: "review " + st.name, Text: brief, Note: true})
	}, func(err error) {
		if err != nil {
			a.toast(nil, "Couldn't brief the reviewer: "+err.Error())
		}
	})
}

// reviewBrief tells a reviewer what to do: read what the agent in pane
// changed (its branch's work, in a worktree), change nothing, send it what
// to fix, review again as it answers, then tell the user its verdict.
func reviewBrief(name, pane string, branch bool) string {
	what, diff := "its uncommitted changes", "omaterm diff "+pane
	if branch {
		what, diff = "what its branch changed", diff+" --branch"
	}
	return fmt.Sprintf("[omaterm] The user asks you to review the work of the agent in session %q (pane %s): you review, it fixes.\n"+
		"1. Read %s: %s\n"+
		"2. Change no files, and commit nothing.\n"+
		"3. Send it what to fix, the most important first: omaterm ask %s \"<what to fix>\" --no-wait\n"+
		"   Then end your turn, without waiting: its answer comes to you as a new message once it has fixed them. Then review again.\n"+
		"4. At most %d rounds. When nothing important is left, or after the last, end with: omaterm report done \"<your verdict, in a line>\"",
		name, pane, what, diff, pane, reviewRounds)
}

// mailJob calls the mailbox's methods off the UI thread, as the omaterm
// command does, then done on it; with no window (tests), at once.
func (a *appState) mailJob(work func(ctx context.Context, call func(method string, params any) error) error, done func(error)) {
	if a.host == nil {
		a.host = newLocalHost(a)
	}
	h := a.host
	run := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return work(ctx, func(method string, params any) error {
			raw, err := json.Marshal(params)
			if err != nil {
				return err
			}
			_, err = h.mail(ctx, method, raw)
			return err
		})
	}
	if a.win == nil {
		done(run())
		return
	}
	go func() {
		err := run()
		a.post(func() { done(err) })
	}()
}

package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"omaterm/internal/client"
	"omaterm/internal/mail"
	"omaterm/internal/proto"
)

// Messages between agents (package mail): omaterm ask types a message
// into another agent's prompt once it is free, and waits for its answer,
// which that agent gives with omaterm reply.

// mailID is a message's id, as omaterm ask prints it.
var mailID = regexp.MustCompile(`^m[0-9]+$`)

// me is the pane the command runs in, and its session's name; "" outside
// omaterm.
func me(st proto.State) (pane, name string) {
	t, err := find(st, current(st))
	if err != nil {
		return "", ""
	}
	if !t.byPane {
		if s, ok := sessionByID(st, t.session); ok {
			t.pane = agentPane(s)
		}
	}
	s, _ := sessionByID(st, t.session)
	return t.pane, s.Name
}

// mine is me, for a command whose answer comes back to its pane: one run
// in a pane omaterm can't tell is refused, as the answer would be lost.
// A Codex's commands carry the variables of whichever pane started its
// shared service: its pane is told only by its hook.
func mine(st proto.State) (pane, name string, err error) {
	pane, name = me(st)
	switch {
	case pane != "":
		return pane, name, nil
	case os.Getenv("CODEX_THREAD_ID") != "":
		return "", "", usageErr("omaterm can't tell which pane this Codex runs in, as its omaterm hook didn't run: " +
			"ask the user to approve it in Codex (/hooks), then to start Codex again")
	case os.Getenv("OMATERM_PANE") != "" || os.Getenv("OMATERM_SESSION") != "":
		return "", "", usageErr("omaterm can't tell which pane you run in: name yours with OMATERM_PANE")
	}
	return "", "", nil // outside omaterm
}

// mailText is a message's text: its words, or "-" for the standard input,
// or a file's.
func mailText(words []string, file string) (string, error) {
	switch {
	case file != "" && len(words) > 0:
		return "", usageErr("the text, or --file, not both")
	case file != "":
		f, err := os.Open(file)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, mail.MaxText+1))
		return string(b), err
	case len(words) == 1 && words[0] == "-":
		return readStdin()
	}
	return strings.Join(words, " "), nil
}

func askCmd(c *client.Client, args []string) error {
	f, err := parse(args, map[string]string{"t": "timeout", "timeout": "timeout", "file": "file"},
		map[string]string{"no-wait": "no-wait", "json": "json", "any-project": "any-project"})
	if err != nil {
		return err
	}
	if len(f.pos) < 1 || len(f.pos) < 2 && f.vals["file"] == "" {
		return usageErr("omaterm ask <target> <text…> [--no-wait] [-t timeout]")
	}
	text, err := mailText(f.pos[1:], f.vals["file"])
	if err != nil {
		return err
	}
	ms, err := parseTimeout(f.vals["timeout"])
	if err != nil {
		return err
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	t, err := find(st, f.pos[0])
	if err != nil {
		return err
	}
	if err := sameProject(st, projectOf(st, t.session), f.set["any-project"]); err != nil {
		return err
	}
	to, _ := sessionByID(st, t.session)
	from, fromName, err := mine(st)
	if err != nil {
		return err
	}
	var m proto.Mail
	if err := c.Call("mail.ask", proto.MailAskParams{From: from, FromName: fromName, To: waitTarget(st, t), ToName: to.Name,
		Text: text, NoWait: f.set["no-wait"]}, &m); err != nil {
		return err
	}
	if f.set["no-wait"] {
		if f.set["json"] {
			return printJSON(m)
		}
		fmt.Println(m.ID)
		return nil
	}
	if err := c.Call("mail.await", proto.MailAwaitParams{ID: m.ID, TimeoutMS: ms}, &m); err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(m)
	}
	switch m.State {
	case proto.MailReplied:
		fmt.Println(strings.TrimRight(m.Reply, "\n"))
	case proto.MailNoReply:
		if m.Screen == "" {
			fmt.Printf("%s stopped without answering: %s.\n", to.Name, m.Why)
		} else {
			fmt.Printf("%s stopped without answering. The end of its screen:\n%s", to.Name, m.Screen)
		}
	default:
		return fmt.Errorf("%s wasn't delivered: %s", m.ID, m.Why)
	}
	return nil
}

func replyCmd(c *client.Client, args []string) error {
	f, err := parse(args, map[string]string{"file": "file"}, nil)
	if err != nil {
		return err
	}
	id, words := "", f.pos
	if len(words) > 0 && mailID.MatchString(words[0]) && (len(words) > 1 || f.vals["file"] != "") {
		id, words = words[0], words[1:]
	}
	if len(words) == 0 && f.vals["file"] == "" {
		return usageErr("omaterm reply [id] <text…> [--file path]")
	}
	text, err := mailText(words, f.vals["file"])
	if err != nil {
		return err
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	from, _ := me(st)
	var m proto.Mail
	if err := c.Call("mail.reply", proto.MailReplyParams{ID: id, From: from, Text: text}, &m); err != nil {
		return err
	}
	if m.FromName == "" {
		fmt.Printf("answered %s\n", m.ID)
	} else {
		fmt.Printf("answered %s from %s\n", m.ID, m.FromName)
	}
	return nil
}

func inboxCmd(c *client.Client, args []string) error {
	f, err := parse(args, nil, map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	pane, _ := me(st)
	var list []proto.Mail
	if err := c.Call("mail.list", proto.MailListParams{Pane: pane}, &list); err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(list)
	}
	fmt.Printf("%-5s %-14s %-14s %-9s %s\n", "ID", "FROM", "TO", "STATE", "TEXT")
	for _, m := range list {
		from := m.FromName
		if m.From == "" {
			from = "omaterm"
		}
		text, _, _ := strings.Cut(strings.TrimSpace(m.Text), "\n")
		fmt.Printf("%-5s %-14s %-14s %-9s %s\n", m.ID, trunc(from, 14), trunc(orDash(m.ToName), 14), m.State, trunc(text, 60))
	}
	return nil
}

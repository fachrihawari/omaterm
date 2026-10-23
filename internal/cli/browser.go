package cli

// omaterm browser drives a browser pane's page. Adapted from Kero
// (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/cli/browser.go.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

// browserCommands are what omaterm browser does to a page, which name no
// target: omaterm browser text is this session's browser's text.
var browserCommands = []string{"open", "back", "forward", "reload", "text", "html", "click", "type", "wait", "eval", "screenshot", "url", "title"}

func browserCmd(c *client.Client, args []string) error {
	f, err := parse(args, map[string]string{"timeout": "timeout", "t": "timeout", "o": "out", "out": "out", "text": "text"},
		map[string]string{"enter": "enter", "E": "enter"})
	if err != nil {
		return err
	}
	const use = "omaterm browser [target] <open|back|forward|reload|text|html|click|type|wait|eval|screenshot|url|title> [args…]"
	st, err := state(c)
	if err != nil {
		return err
	}
	pos := f.pos
	var t target
	if len(pos) > 0 && !slices.Contains(browserCommands, pos[0]) {
		if t, err = find(st, pos[0]); err != nil {
			return err
		}
		pos = pos[1:]
	} else {
		if t, err = findOrCurrent(st, nil); err != nil {
			return usageErr("which browser? (outside omaterm, name one): %s", use)
		}
		// The pane a command runs in is a terminal: its session's browser
		// is meant.
		t.pane, t.byPane = "", false
	}
	if len(pos) == 0 {
		return usageErr("%s", use)
	}
	op, pos := pos[0], pos[1:]
	if !slices.Contains(browserCommands, op) {
		return usageErr("no browser command %q: %s", op, use)
	}
	pane, err := browserOf(st, t)
	if err != nil {
		return err
	}
	timeout, err := parseTimeout(f.vals["timeout"])
	if err != nil {
		return err
	}
	p := proto.BrowserParams{ID: pane, Op: op, TimeoutMS: timeout}
	rest := func(from []string) (string, error) {
		text := strings.Join(append(from, f.rest...), " ")
		if text == "-" {
			b, err := io.ReadAll(os.Stdin)
			return string(b), err
		}
		return text, nil
	}
	switch op {
	case "open":
		if p.Text, err = rest(pos); err != nil {
			return err
		}
		if strings.TrimSpace(p.Text) == "" {
			return usageErr("omaterm browser [target] open <url>")
		}
	case "back", "forward", "reload", "screenshot", "url", "title":
		if len(pos) > 0 || len(f.rest) > 0 {
			return usageErr("omaterm browser [target] %s takes no arguments", op)
		}
	case "text", "html":
		if len(pos) > 1 {
			return usageErr("omaterm browser [target] %s [selector]", op)
		}
		if len(pos) == 1 {
			p.Selector = pos[0]
		}
	case "click":
		if len(pos) != 1 {
			return usageErr("omaterm browser [target] click <selector>")
		}
		p.Selector = pos[0]
	case "type":
		if len(pos) < 1 {
			return usageErr("omaterm browser [target] type <selector> <text…> [--enter]")
		}
		p.Selector, p.Enter = pos[0], f.set["enter"]
		if p.Text, err = rest(pos[1:]); err != nil {
			return err
		}
	case "wait":
		if len(pos) > 1 {
			return usageErr("omaterm browser [target] wait [selector] [--text text]")
		}
		if len(pos) == 1 {
			p.Selector = pos[0]
		}
		p.Text = f.vals["text"]
	case "eval":
		if p.Text, err = rest(pos); err != nil {
			return err
		}
		if strings.TrimSpace(p.Text) == "" {
			return usageErr("omaterm browser [target] eval <js…>")
		}
	}
	if f.vals["out"] != "" && op != "screenshot" {
		return usageErr("-o is screenshot's")
	}
	if f.set["enter"] && op != "type" {
		return usageErr("--enter is type's")
	}
	if f.vals["text"] != "" && op != "wait" {
		return usageErr("--text is wait's")
	}
	var res proto.BrowserResult
	if err := c.Call("browser", p, &res); err != nil {
		return err
	}
	if op == "screenshot" {
		return saveShot(res.PNG, pane, f.vals["out"])
	}
	if res.Text != "" {
		fmt.Print(res.Text)
		if !strings.HasSuffix(res.Text, "\n") {
			fmt.Println()
		}
	}
	return nil
}

// browserOf is the browser pane a target stands for: the pane named, or
// for a session, its focused pane when that is a browser, else its one
// browser.
func browserOf(st proto.State, t target) (string, error) {
	var s *proto.Session
	for i := range st.Sessions {
		if st.Sessions[i].ID == t.session {
			s = &st.Sessions[i]
		}
	}
	if s == nil {
		return "", fmt.Errorf("%w: no session %s", errNotFound, t.session)
	}
	paneOf := func(id string) *proto.Pane {
		for i := range s.Panes {
			if s.Panes[i].ID == id {
				return &s.Panes[i]
			}
		}
		return nil
	}
	if t.byPane {
		if p := paneOf(t.pane); p == nil || p.URL == "" {
			return "", fmt.Errorf("%w: pane %s is a terminal, not a browser", errNotFound, t.pane)
		}
		return t.pane, nil
	}
	if p := paneOf(s.Focus); p != nil && p.URL != "" {
		return p.ID, nil
	}
	var ids []string
	for _, p := range s.Panes {
		if p.URL != "" {
			ids = append(ids, p.ID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("%w: session %s has no browser pane (open one with omaterm split --url <url>)", errNotFound, s.ID)
	}
	return "", fmt.Errorf("%w: session %s has several browser panes, name one: %s", errNotFound, s.ID, strings.Join(ids, ", "))
}

// saveShot writes a screenshot where -o says, - for stdout, else to a
// file of its own in the temporary directory, readable by its owner only,
// and prints where it went.
func saveShot(png []byte, pane, out string) error {
	if len(png) == 0 {
		return errors.New("the page gave no picture")
	}
	if out == "-" {
		_, err := os.Stdout.Write(png)
		return err
	}
	if out == "" {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("omaterm-%s-%s.png", pane, time.Now().Format("20060102-150405.000")))
	}
	if err := os.WriteFile(out, png, 0o600); err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

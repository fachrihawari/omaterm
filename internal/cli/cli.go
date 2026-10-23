// Package cli is the omaterm command: `omaterm <command>` drives the
// running app's sessions over its control socket, as agents do to run
// things beside them, wait for them, and read their screens.
//
// Its commands, flags and exit statuses follow Kero's kero command
// (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE), so agents'
// knowledge of one carries to the other.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/osc"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
)

// Commands are the omaterm command's subcommands; any other first argument
// starts the app.
var Commands = []string{
	"ls", "list", "projects", "new", "split", "send", "key", "prompt", "read", "capture", "wait", "status",
	"rename", "focus", "close", "kill", "restart", "notify", "report", "skill", "hook", "cli", "do", "actions",
	"diff", "browser", "daemon", "clients", "help", "version", "--help", "-h", "--version",
}

// IsCommand tells a subcommand from a path to open.
func IsCommand(arg string) bool { return slices.Contains(Commands, arg) }

// Version is the app's version, set by main.
var Version = "dev"

// Errors of exit statuses other than 1.
var (
	errUsage       = errors.New("usage")
	errNotFound    = errors.New("not found")
	errUnreachable = errors.New("unreachable")
)

// exitStatus is a waited-for program's exit status, which omaterm exits
// with.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// Run runs a subcommand, returning the exit code: 0 ok, 1 failed, 2 bad
// usage, 3 no such target, 4 omaterm not running.
func Run(args []string) int {
	err := run(args)
	if err == nil {
		return 0
	}
	var es exitStatus
	if errors.As(err, &es) {
		return int(es)
	}
	fmt.Fprintln(os.Stderr, "omaterm:", err)
	switch {
	case errors.Is(err, errUsage) || proto.CodeOf(err) == proto.CodeUsage:
		return 2
	case errors.Is(err, errNotFound) || proto.CodeOf(err) == proto.CodeNotFound:
		return 3
	case errors.Is(err, errUnreachable) || errors.Is(err, client.ErrUnreachable) || proto.CodeOf(err) == proto.CodeAppClosed:
		return 4
	}
	return 1
}

func usageErr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{errUsage}, args...)...)
}

// flags parses flags anywhere among the arguments: names that take a
// value, and booleans; the rest are positional, all of them after "--".
type flags struct {
	vals map[string]string
	set  map[string]bool
	pos  []string
	rest []string // after --
}

func parse(args []string, valued, boolean map[string]string) (*flags, error) {
	f := &flags{vals: map[string]string{}, set: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.rest = args[i+1:]
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			f.pos = append(f.pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if canon, ok := valued[name]; ok {
			if !hasVal {
				if i+1 >= len(args) {
					return nil, usageErr("%s needs a value", a)
				}
				i++
				val = args[i]
			}
			f.vals[canon] = val
			continue
		}
		if canon, ok := boolean[name]; ok {
			f.set[canon] = true
			continue
		}
		return nil, usageErr("unknown flag %s", a)
	}
	return f, nil
}

func connect() (*client.Client, error) {
	c, err := client.Connect()
	if errors.Is(err, client.ErrUnreachable) {
		// The window is closed: the session daemon, which runs the
		// sessions, answers for them.
		c, err = client.Dial(paths.DaemonSocketPath())
	}
	if err != nil {
		if errors.Is(err, client.ErrUnreachable) {
			return nil, fmt.Errorf("%w: omaterm is not running", errUnreachable)
		}
		return nil, err
	}
	return c, nil
}

func state(c *client.Client) (proto.State, error) {
	var st proto.State
	err := c.Call("state", nil, &st)
	return st, err
}

func run(args []string) error {
	if len(args) == 0 {
		return usageErr("omaterm <command>; omaterm help lists them")
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "help", "--help", "-h":
		fmt.Print(helpText)
		return nil
	case "version", "--version":
		fmt.Println("omaterm", Version)
		return nil
	case "report":
		return report(args)
	case "skill":
		return skillCmd(args)
	case "hook":
		return hookCmd(args)
	case "cli":
		return cliCmd(args)
	case "daemon":
		return daemonCmd(args)
	case "clients":
		return clientsCmd(args)
	}

	c, err := connect()
	if err != nil {
		return err
	}
	defer c.Close()

	switch cmd {
	case "ls", "list":
		f, err := parse(args, nil, map[string]string{"json": "json"})
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		if f.set["json"] {
			return printJSON(st.Sessions)
		}
		listSessions(st)
		return nil

	case "projects":
		f, err := parse(args, nil, map[string]string{"json": "json"})
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		if f.set["json"] {
			return printJSON(st.Projects)
		}
		for _, p := range st.Projects {
			fmt.Printf("%s  %-20s %s\n", p.ID, p.Name, shortDir(p.Dir))
		}
		return nil

	case "new", "split":
		valued := map[string]string{"p": "project", "project": "project", "n": "name", "name": "name", "d": "dir", "dir": "dir",
			"t": "timeout", "timeout": "timeout", "r": "ratio", "ratio": "ratio", "u": "url", "url": "url"}
		boolean := map[string]string{"f": "focus", "focus": "focus", "w": "wait", "wait": "wait", "json": "json",
			"right": "right", "down": "down", "left": "left", "up": "up"}
		f, err := parse(args, valued, boolean)
		if err != nil {
			return err
		}
		p := proto.CreateParams{Project: f.vals["project"], Name: f.vals["name"], Dir: f.vals["dir"], Command: f.rest,
			Focus: f.set["focus"], URL: f.vals["url"]}
		if p.Dir == "" {
			p.Dir, _ = os.Getwd()
		} else if abs, err := filepath.Abs(p.Dir); err == nil {
			p.Dir = abs
		}
		if cmd == "split" {
			st, err := state(c)
			if err != nil {
				return err
			}
			t, err := findOrCurrent(st, f.pos)
			if err != nil {
				return err
			}
			p.Split = t.pane
			p.Direction = "right"
			for _, d := range []string{"down", "left", "up"} {
				if f.set[d] {
					p.Direction = d
				}
			}
			if r := f.vals["ratio"]; r != "" {
				v, err := strconv.ParseFloat(r, 64)
				if err != nil || v <= 0 || v >= 1 {
					return usageErr("--ratio is between 0 and 1")
				}
				p.Ratio = v
			}
			if f.vals["dir"] == "" {
				p.Dir = ""
			}
		} else if len(f.pos) > 0 {
			return usageErr("omaterm new [-n name] [-d dir] [-- command…]")
		}
		method := "create"
		if cmd == "split" {
			method = "split"
		}
		var out proto.Created
		if err := c.Call(method, p, &out); err != nil {
			return err
		}
		if f.set["json"] {
			return printJSON(out)
		}
		if cmd == "split" {
			fmt.Println(out.Pane)
		} else {
			fmt.Println(out.Session)
		}
		if f.set["wait"] {
			return waitExit(c, out.Pane, f.vals["timeout"])
		}
		return nil

	case "send":
		f, err := parse(args, nil, map[string]string{"e": "escapes", "enter": "enter"})
		if err != nil {
			return err
		}
		if len(f.pos) < 1 {
			return usageErr("omaterm send <target> <text…> [--enter] [-e]")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, f.pos[0])
		if err != nil {
			return err
		}
		text := strings.Join(append(f.pos[1:], f.rest...), " ")
		if text == "-" {
			b, err := readStdin()
			if err != nil {
				return err
			}
			text = b
		}
		if f.set["escapes"] {
			text = unescape(text)
		}
		if text != "" {
			if err := c.Call("send", proto.SendParams{ID: t.pane, Data: text}, nil); err != nil {
				return err
			}
		}
		if f.set["enter"] {
			// Programs take a paste and an Enter in the same read as a
			// newline in the text: Enter comes on its own.
			time.Sleep(120 * time.Millisecond)
			return c.Call("send", proto.SendParams{ID: t.pane, Data: "\r"}, nil)
		}
		return nil

	case "key":
		if len(args) < 2 {
			return usageErr("omaterm key <target> <key…>  (enter, tab, escape, up, ctrl+c, alt+x…)")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, args[0])
		if err != nil {
			return err
		}
		var data []byte
		for _, k := range args[1:] {
			b, err := keyBytes(k)
			if err != nil {
				return err
			}
			data = append(data, b...)
		}
		return c.Call("send", proto.SendParams{ID: t.pane, Data: string(data)}, nil)

	case "prompt":
		f, err := parse(args, map[string]string{"t": "timeout", "timeout": "timeout"}, nil)
		if err != nil {
			return err
		}
		if len(f.pos) < 2 {
			return usageErr("omaterm prompt <target> <text…> [-t timeout]")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, f.pos[0])
		if err != nil {
			return err
		}
		ms, err := parseTimeout(f.vals["timeout"])
		if err != nil {
			return err
		}
		if err := c.Call("send", proto.SendParams{ID: t.pane, Data: strings.Join(f.pos[1:], " ")}, nil); err != nil {
			return err
		}
		time.Sleep(120 * time.Millisecond)
		if err := c.Call("send", proto.SendParams{ID: t.pane, Data: "\r"}, nil); err != nil {
			return err
		}
		if err := c.Call("wait", proto.WaitParams{ID: t.pane, TimeoutMS: ms}, nil); err != nil {
			return err
		}
		var out proto.ReadResult
		if err := c.Call("read", proto.ReadParams{ID: t.pane}, &out); err != nil {
			return err
		}
		fmt.Print(out.Text)
		return nil

	case "read", "capture":
		f, err := parse(args, map[string]string{"n": "lines", "lines": "lines"}, map[string]string{"a": "all", "all": "all"})
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := findOrCurrent(st, f.pos)
		if err != nil {
			return err
		}
		n := 0
		if v := f.vals["lines"]; v != "" {
			if n, err = strconv.Atoi(v); err != nil || n < 1 {
				return usageErr("-n takes a number of lines")
			}
		}
		var out proto.ReadResult
		if err := c.Call("read", proto.ReadParams{ID: t.pane, Scrollback: f.set["all"], Lines: n}, &out); err != nil {
			return err
		}
		fmt.Print(out.Text)
		return nil

	case "wait":
		f, err := parse(args, map[string]string{"for": "for", "t": "timeout", "timeout": "timeout"},
			map[string]string{"x": "exit", "exit": "exit", "json": "json"})
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := findOrCurrent(st, f.pos)
		if err != nil {
			return err
		}
		if f.set["exit"] {
			return waitExit(c, t.pane, f.vals["timeout"])
		}
		ms, err := parseTimeout(f.vals["timeout"])
		if err != nil {
			return err
		}
		p := proto.WaitParams{ID: t.pane, TimeoutMS: ms}
		if v := f.vals["for"]; v != "" {
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if !slices.Contains([]string{"idle", "working", "waiting", "error", "exited"}, s) {
					return usageErr("--for takes idle, working, waiting, error, exited")
				}
				p.Status = append(p.Status, s)
			}
		}
		var info proto.Pane
		if err := c.Call("wait", p, &info); err != nil {
			return err
		}
		if f.set["json"] {
			return printJSON(info)
		}
		fmt.Println(info.Status)
		if info.Status == "exited" && info.ExitCode != nil && *info.ExitCode != 0 {
			return exitStatus(*info.ExitCode)
		}
		return nil

	case "status":
		f, err := parse(args, nil, map[string]string{"json": "json"})
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := findOrCurrent(st, f.pos)
		if err != nil {
			return err
		}
		info := t.info(st)
		if f.set["json"] {
			return printJSON(info)
		}
		fmt.Printf("%s  %s  %s  %s\n", info.Status, orDash(info.Agent+info.Process), shortDir(info.WorkDir()), info.Message)
		return nil

	case "rename":
		if len(args) < 1 {
			return usageErr("omaterm rename <session> <name…>")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, args[0])
		if err != nil {
			return err
		}
		return c.Call("rename", proto.RenameParams{ID: t.session, Name: strings.Join(args[1:], " "), By: os.Getenv("OMATERM_PANE")}, nil)

	case "focus":
		if len(args) != 1 {
			return usageErr("omaterm focus <target>")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, args[0])
		if err != nil {
			return err
		}
		return c.Call("focus", proto.IDParams{ID: t.pane}, nil)

	case "close", "kill":
		if len(args) != 1 {
			return usageErr("omaterm close <target>")
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := find(st, args[0])
		if err != nil {
			return err
		}
		if t.byPane {
			return c.Call("pane.close", proto.IDParams{ID: t.pane}, nil)
		}
		return c.Call("close", proto.IDParams{ID: t.session}, nil)

	case "restart":
		st, err := state(c)
		if err != nil {
			return err
		}
		t, err := findOrCurrent(st, args)
		if err != nil {
			return err
		}
		return c.Call("restart", proto.IDParams{ID: t.pane}, nil)

	case "notify":
		f, err := parse(args, map[string]string{"s": "target", "session": "target", "title": "title"}, nil)
		if err != nil {
			return err
		}
		st, err := state(c)
		if err != nil {
			return err
		}
		var pos []string
		if v := f.vals["target"]; v != "" {
			pos = []string{v}
		}
		t, err := findOrCurrent(st, pos)
		if err != nil {
			return err
		}
		return c.Call("notify", proto.NotifyParams{ID: t.pane, Title: f.vals["title"], Body: strings.Join(append(f.pos, f.rest...), " ")}, nil)

	case "do":
		if len(args) != 1 {
			return usageErr("omaterm do <action>; omaterm actions lists them")
		}
		return c.Call("do", proto.DoParams{Action: args[0]}, nil)

	case "actions":
		var list []struct{ ID, Title string }
		if err := c.Call("actions", nil, &list); err != nil {
			return err
		}
		for _, a := range list {
			fmt.Printf("%-28s %s\n", a.ID, a.Title)
		}
		return nil

	case "diff":
		return diffCmd(c, args)

	case "browser":
		return browserCmd(c, args)
	}
	return usageErr("unknown command %q; omaterm help lists them", cmd)
}

// target is what a command acts on: a session, and its pane (the focused
// one when named by its session).
type target struct {
	session, pane string
	byPane        bool
}

func (t target) info(st proto.State) proto.Pane {
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			if p.ID == t.pane {
				return p
			}
		}
	}
	return proto.Pane{ID: t.pane}
}

// find resolves a target: a pane or session id or a prefix of one, or a
// session's name. A session stands for its focused pane.
func find(st proto.State, ref string) (target, error) {
	if ref == "" {
		return target{}, usageErr("no target")
	}
	focusOf := func(s proto.Session) string {
		if s.Focus != "" {
			return s.Focus
		}
		if len(s.Panes) > 0 {
			return s.Panes[0].ID
		}
		return ""
	}
	var exact, prefix []target
	for _, s := range st.Sessions {
		switch {
		case s.ID == ref, strings.EqualFold(s.Name, ref):
			exact = append(exact, target{session: s.ID, pane: focusOf(s)})
		case strings.HasPrefix(s.ID, ref):
			prefix = append(prefix, target{session: s.ID, pane: focusOf(s)})
		}
		for _, p := range s.Panes {
			switch {
			case p.ID == ref:
				exact = append(exact, target{session: s.ID, pane: p.ID, byPane: true})
			case strings.HasPrefix(p.ID, ref):
				prefix = append(prefix, target{session: s.ID, pane: p.ID, byPane: true})
			}
		}
	}
	for _, list := range [][]target{exact, prefix} {
		switch len(list) {
		case 0:
			continue
		case 1:
			return list[0], nil
		}
		var names []string
		for _, t := range list {
			names = append(names, t.pane)
		}
		return target{}, fmt.Errorf("%w: %q could be %s", errNotFound, ref, strings.Join(names, ", "))
	}
	return target{}, fmt.Errorf("%w: no session or pane %q", errNotFound, ref)
}

// current is the target of a command run in a pane with none named: the
// pane itself.
func current() string {
	if p := os.Getenv("OMATERM_PANE"); p != "" {
		return p
	}
	return os.Getenv("OMATERM_SESSION")
}

func findOrCurrent(st proto.State, pos []string) (target, error) {
	if len(pos) > 0 {
		return find(st, pos[0])
	}
	if cur := current(); cur != "" {
		return find(st, cur)
	}
	return target{}, usageErr("name a target, or run this in an omaterm pane")
}

func listSessions(st proto.State) {
	projects := map[string]string{}
	for _, p := range st.Projects {
		projects[p.ID] = p.Name
	}
	fmt.Printf("%-8s %-8s %-22s %-9s %-14s %s\n", "SESSION", "PANE", "NAME", "STATUS", "PROGRAM", "DIR")
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			name := projects[s.Project] + "/" + s.Name
			fmt.Printf("%-8s %-8s %-22s %-9s %-14s %s\n", s.ID, p.ID, trunc(name, 22), p.Status,
				trunc(orDash(firstNonEmpty(p.Agent, p.Process)), 14), shortDir(p.WorkDir()))
		}
	}
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortDir(d string) string {
	home := paths.Home()
	if d == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(d, home+"/"); ok {
		return "~/" + rest
	}
	return d
}

func parseTimeout(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		n, err2 := strconv.Atoi(s)
		if err2 != nil {
			return 0, usageErr("bad timeout %q", s)
		}
		d = time.Duration(n) * time.Second
	}
	return d.Milliseconds(), nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func readStdin() (string, error) {
	var b strings.Builder
	buf := make([]byte, 64<<10)
	for {
		n, err := os.Stdin.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String(), nil
		}
	}
}

// keys are the names omaterm key takes, as the bytes a terminal sends.
var keys = map[string]string{
	"enter": "\r", "return": "\r", "tab": "\t", "escape": "\x1b", "esc": "\x1b", "space": " ",
	"backspace": "\x7f", "delete": "\x1b[3~", "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C",
	"left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F", "pageup": "\x1b[5~", "pagedown": "\x1b[6~",
	"shift+tab": "\x1b[Z",
}

func keyBytes(name string) ([]byte, error) {
	n := strings.ToLower(name)
	if b, ok := keys[n]; ok {
		return []byte(b), nil
	}
	if rest, ok := strings.CutPrefix(n, "ctrl+"); ok && len(rest) == 1 {
		c := rest[0]
		switch {
		case c >= 'a' && c <= 'z':
			return []byte{c - 'a' + 1}, nil
		case c >= '@' && c <= '_':
			return []byte{c - '@'}, nil
		}
	}
	if rest, ok := strings.CutPrefix(n, "alt+"); ok {
		b, err := keyBytes(rest)
		if err != nil {
			return nil, err
		}
		return append([]byte{0x1b}, b...), nil
	}
	if len([]rune(name)) == 1 {
		return []byte(name), nil
	}
	return nil, usageErr("unknown key %q", name)
}

// waitExit waits for the command a pane started with to end, and exits
// with its status: or its shell's, if that ended first.
func waitExit(c *client.Client, pane, timeout string) error {
	ms, err := parseTimeout(timeout)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var info proto.Pane
	if err := c.CallContext(ctx, "wait", proto.WaitParams{ID: pane, Exit: true, TimeoutMS: ms}, &info); err != nil {
		return err
	}
	code := 0
	if info.ExitCode != nil {
		code = *info.ExitCode
	}
	if info.CommandExit != nil {
		code = *info.CommandExit
	}
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

// report writes an OSC 7501 report of the program's status to the
// terminal, as a script reports what it does: omaterm shows it, and so do
// other terminals that read the protocol.
func report(args []string) error {
	f, err := parse(args, map[string]string{"kind": "kind", "progress": "progress", "app": "app", "id": "id", "title": "title"}, nil)
	if err != nil {
		return err
	}
	if len(f.pos) < 1 {
		return usageErr("omaterm report <idle|working|done|blocked|error|clear> [message]")
	}
	r := osc.Record{State: f.pos[0], Progress: -1, ID: f.vals["id"], Kind: f.vals["kind"], App: f.vals["app"], Title: f.vals["title"]}
	if !slices.Contains([]string{"idle", "working", "done", "blocked", "error", "clear"}, r.State) {
		return usageErr("unknown state %q", r.State)
	}
	if v := f.vals["progress"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 100 {
			return usageErr("--progress takes 0 to 100")
		}
		r.Progress = n
	}
	r.Msg = strings.Join(append(f.pos[1:], f.rest...), " ")
	seq := osc.Encode(r)
	// To the terminal, even when the output goes elsewhere.
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		defer tty.Close()
		_, err = tty.WriteString(seq)
		return err
	}
	_, err = os.Stdout.WriteString(seq)
	return err
}

// unescape interprets the escapes of echo -e: \n \r \t \e \a \b \\ \xHH.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case '\\':
			b.WriteByte('\\')
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// InstallCLI links the omaterm command into ~/.local/bin, as `omaterm
// cli install` does, returning where.
func InstallCLI() (string, error) {
	link := filepath.Join(paths.Home(), ".local", "bin", "omaterm")
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%s exists and is not a link: left alone", shortDir(link))
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return "", err
	}
	os.Remove(link)
	return shortDir(link), os.Symlink(exe, link)
}

// cliCmd installs the omaterm command in ~/.local/bin, for shells outside
// omaterm.
func cliCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	link := filepath.Join(paths.Home(), ".local", "bin", "omaterm")
	switch sub {
	case "status":
		if t, err := os.Readlink(link); err == nil {
			fmt.Printf("%s → %s\n", shortDir(link), t)
		} else if _, err := os.Stat(link); err == nil {
			fmt.Printf("%s is a file of its own\n", shortDir(link))
		} else {
			fmt.Printf("not installed (omaterm cli install puts it in %s)\n", shortDir(filepath.Dir(link)))
		}
		return nil
	case "install":
		where, err := InstallCLI()
		if err != nil {
			return err
		}
		fmt.Printf("installed %s\n", where)
		return nil
	case "uninstall":
		if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return os.Remove(link)
		}
		return nil
	}
	return usageErr("omaterm cli install|uninstall|status")
}

const helpText = `omaterm — drive the sessions of the running omaterm app

  omaterm [dir]                          open the app, or a folder in it
  omaterm ls [--json]                    sessions and their panes
  omaterm projects [--json]
  omaterm new [-n name] [-d dir] [-p project] [-f] [-w] [-- command…]
  omaterm split [target] [--down|--left|--up] [-r ratio] [-w] [--url url] [-- command…]
  omaterm send <target> <text…> [--enter] [-e]
  omaterm key <target> <key…>            enter, tab, escape, up, ctrl+c, alt+x…
  omaterm prompt <target> <text…>        send, Enter, wait, print the screen
  omaterm read [target] [-n lines] [-a]
  omaterm wait [target] [--for idle,waiting,error,exited] [--exit] [-t timeout]
  omaterm status [target] [--json]
  omaterm rename <session> <name…>
  omaterm focus | close | restart <target>
  omaterm notify [message…]
  omaterm report <idle|working|done|blocked|error|clear> [message] [--kind] [--progress]
  omaterm diff [target] [--turn|--branch] [path…]
  omaterm browser [target] <open|text|html|click|type|wait|eval|screenshot|back|forward|reload|url|title> …
  omaterm skill setup|status|show|remove [--agent claude|codex|pi|opencode]
  omaterm cli install|uninstall|status
  omaterm daemon status|stop|restart|install-service|uninstall-service
  omaterm clients [--json]               who the session daemon serves
  omaterm do <action>, omaterm actions
  omaterm version

A target is a session or pane id (a prefix works) or a session's name; in
an omaterm pane it defaults to the pane itself ($OMATERM_PANE).

With the window closed, the session daemon answers for the sessions;
focus, browser and do need the window.

Exit status: 0 ok, 1 failed, 2 bad usage, 3 no such target, 4 omaterm
not running (or the window is closed, for what needs it); wait --exit and
--wait exit with the command's status.
`

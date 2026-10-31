package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

// processTimeout is how long start --wait and restart --wait hold by default.
const processTimeout = 60 * time.Second

// psCmd lists the processes of the project the command runs in, with their
// status, port and restarts.
func psCmd(c *client.Client, args []string) error {
	f, err := parse(args, nil, map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	if len(f.pos) > 0 {
		return usageErr("omaterm ps [--json]")
	}
	project, err := projectNow(c)
	if err != nil {
		return err
	}
	list, err := processList(c, project)
	if err != nil {
		return err
	}
	if f.set["json"] {
		if list == nil {
			list = []proto.Process{}
		}
		return printJSON(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no processes defined: add one in omaterm (Processes → Add)")
		return nil
	}
	fmt.Printf("%-16s %-10s %-6s %-7s %-9s %-28s %s\n", "NAME", "STATUS", "PORT", "PID", "RESTARTS", "URL", "MESSAGE")
	for _, p := range list {
		fmt.Printf("%-16s %-10s %-6s %-7s %-9s %-28s %s\n", fit(p.Name, 16), p.Status,
			dashInt(p.Port), dashInt(p.PID), dashInt(p.Restarts), fit(dash(processAddress(p)), 28), dash(p.Message))
	}
	return nil
}

// processCmd starts, stops or restarts a process of the project the command
// runs in. With --wait (start and restart only) it holds until the process is
// up, or has failed.
func processCmd(c *client.Client, method string, args []string) error {
	verb := strings.TrimPrefix(method, "process.")
	vals, bools := map[string]string{"timeout": "timeout", "t": "timeout"}, map[string]string{"wait": "wait"}
	usage := fmt.Sprintf("omaterm %s <name> [--wait] [--timeout 60s]", verb)
	if method == "process.stop" {
		vals, bools = nil, nil
		usage = "omaterm stop <name>"
	}
	f, err := parse(args, vals, bools)
	if err != nil {
		return err
	}
	if len(f.pos) != 1 {
		return usageErr("%s", usage)
	}
	timeout, err := waitTimeout(f.vals["timeout"])
	if err != nil {
		return err
	}
	project, err := projectNow(c)
	if err != nil {
		return err
	}
	name := f.pos[0]
	var p proto.Process
	if err := c.Call(method, proto.ProcessParams{Project: project, Name: name}, &p); err != nil {
		return err
	}
	if !f.set["wait"] {
		fmt.Printf("%s is %s\n", name, p.Status)
		return nil
	}
	return waitProcess(c, project, name, timeout, f.vals["timeout"])
}

// restartProcess restarts the project process the arguments name, and reports
// whether there was one. Otherwise restart keeps its meaning for a session or
// a pane.
func restartProcess(c *client.Client, args []string) (bool, error) {
	f, err := parse(args, map[string]string{"timeout": "timeout", "t": "timeout"}, map[string]string{"wait": "wait"})
	if err != nil || len(f.pos) != 1 {
		return false, nil
	}
	project, err := projectNow(c)
	if err != nil {
		return false, nil
	}
	list, err := processList(c, project)
	if err != nil {
		return false, nil
	}
	for _, p := range list {
		if p.Name == f.pos[0] {
			return true, processCmd(c, "process.restart", args)
		}
	}
	return false, nil
}

// waitProcess polls the project's processes until the one named is running,
// or has exited with code 0. It fails when the process has failed, or when
// the timeout runs out first.
func waitProcess(c *client.Client, project, name string, timeout time.Duration, label string) error {
	if label == "" {
		label = processTimeout.String()
	}
	deadline := time.Now().Add(timeout)
	for {
		list, err := processList(c, project)
		if err != nil {
			return err
		}
		p, ok := findProcess(list, name)
		if !ok {
			return fmt.Errorf("no process %q", name)
		}
		switch {
		case p.Status == procs.Running, p.Status == procs.Exited && p.LastExit == 0:
			fmt.Printf("%s is %s\n", name, p.Status)
			return nil
		case p.Status == procs.Failed, p.Status == procs.Paused, p.Status == procs.Stopped,
			p.Status == procs.Untrusted, p.Status == procs.Exited:
			if p.Message == "" {
				return fmt.Errorf("%s is %s", name, p.Status)
			}
			return errors.New(p.Message)
		case p.Status == procs.Starting && p.Restarts > 0 && p.LastExit != 0:
			// It exited and is being restarted: waiting through the
			// backoff would hide the exit the user needs to see.
			return fmt.Errorf("%s exited (code %d) and is restarting", name, p.LastExit)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s didn't come up in %s", name, label)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// waitTimeout reads --timeout, which defaults to processTimeout.
func waitTimeout(s string) (time.Duration, error) {
	ms, err := parseTimeout(s)
	if err != nil {
		return 0, err
	}
	if ms == 0 {
		return processTimeout, nil
	}
	return time.Duration(ms) * time.Millisecond, nil
}

// processList asks the daemon for a project's processes.
func processList(c *client.Client, project string) ([]proto.Process, error) {
	var list []proto.Process
	err := c.Call("process.list", proto.ProcessParams{Project: project}, &list)
	return list, err
}

// findProcess is the process of the name, from a list of them.
func findProcess(list []proto.Process, name string) (proto.Process, bool) {
	for _, p := range list {
		if p.Name == name {
			return p, true
		}
	}
	return proto.Process{}, false
}

// projectNow is the id of the project the command runs in.
func projectNow(c *client.Client) (string, error) {
	st, err := state(c)
	if err != nil {
		return "", err
	}
	return currentProject(st)
}

// currentProject is the project a process command acts on: the one of the
// session it runs in, else the project whose folder holds the working
// directory, the longest folder winning.
func currentProject(st proto.State) (string, error) {
	if s := os.Getenv("OMATERM_SESSION"); s != "" {
		if id := projectOf(st, s); id != "" {
			return id, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	best, bestDir := "", ""
	for _, p := range st.Projects {
		if within(wd, p.Dir) && len(p.Dir) > len(bestDir) {
			best, bestDir = p.ID, p.Dir
		}
	}
	if best == "" {
		return "", errors.New("not in an omaterm project")
	}
	return best, nil
}

// within reports whether path is dir, or inside it.
func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dash is a cell's text, or "-" for an empty one.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// dashInt is a cell's number, or "-" for zero.
func dashInt(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// processAddress is where a process is reached: its Dev URL while routed,
// else its share, else "".
func processAddress(p proto.Process) string {
	if p.Link != "" {
		return p.Link
	}
	return p.Share
}

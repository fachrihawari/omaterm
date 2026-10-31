package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/devurl"
	"omaterm/internal/paths"
)

// proxyCmd looks after Dev URLs: the proxy that serves a process at
// https://<name>.localhost, and the one-time setup that makes a browser
// trust it.
func proxyCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	env := devurl.DefaultEnv(filepath.Join(paths.StateDir(), "devurl"))
	switch sub {
	case "status":
		return proxyStatus(env, args)
	case "setup":
		return proxySetup(env, args)
	case "uninstall":
		return proxyUninstall(env, args)
	}
	return usageErr("omaterm proxy status [--json] | setup [--dry-run] | uninstall")
}

// proxyCall is how the daemon is asked: a variable for the tests.
var proxyCall = daemon.CallProxy

// proxyState asks the daemon how the proxy is. With no daemon running it
// answers with the checks as this process makes them.
func proxyState(env devurl.Env, method string, start bool) (daemon.ProxyStatus, error) {
	st, err := proxyCall(method, start)
	if errors.Is(err, client.ErrUnreachable) {
		reason := "the session daemon isn't running: open omaterm"
		return daemon.ProxyStatus{Error: reason, Checks: devurl.Checks(env, devurl.ProxyState{Error: reason})}, nil
	}
	return st, err
}

func proxyStatus(env devurl.Env, args []string) error {
	f, err := parse(args, nil, map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	st, err := proxyState(env, "proxy.status", false)
	if err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(st)
	}
	printChecks(st.Checks)
	if len(st.Routes) == 0 {
		fmt.Println("no process has a url: give one a url in omaterm.yml")
		return nil
	}
	fmt.Println()
	for _, r := range st.Routes {
		state := "not running"
		if r.Running {
			state = "running, port " + strconv.Itoa(r.Port)
		}
		fmt.Printf("%-28s %s in %s (%s)\n", r.Host, r.Process, r.Project, state)
	}
	return nil
}

func printChecks(checks []devurl.Check) {
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		if c.Detail != "" {
			fmt.Printf("%s %s: %s\n", mark, c.Name, c.Detail)
		} else {
			fmt.Printf("%s %s\n", mark, c.Name)
		}
	}
}

// printPlan tells what setup changes, and who it asks for the password.
func printPlan(plan []devurl.Change) {
	for _, c := range plan {
		switch {
		case c.Blocked != "":
			fmt.Printf("  ✗ %s: can't, %s\n", c.Text, c.Blocked)
		case c.Admin:
			fmt.Printf("  • %s (asks for your password)\n", c.Text)
		default:
			fmt.Printf("  • %s\n", c.Text)
		}
	}
}

func proxySetup(env devurl.Env, args []string) error {
	f, err := parse(args, map[string]string{"ca": "ca"}, map[string]string{"dry-run": "dry-run", "system": "system"})
	if err != nil {
		return err
	}
	if f.set["system"] {
		// The step that runs as root: nothing but the system's part.
		if f.vals["ca"] == "" {
			return usageErr("omaterm proxy setup --system --ca <ca.pem>")
		}
		return devurl.ApplySystem(env, f.vals["ca"])
	}
	plan, err := devurl.Plan(env)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		fmt.Println("Dev URLs is already set up.")
	} else {
		fmt.Println("Setting up Dev URLs will:")
		printPlan(plan)
	}
	if f.set["dry-run"] {
		return nil
	}
	if err := devurl.ApplyUser(env, plan); err != nil {
		return err
	}
	if admin(plan) {
		exe, err := selfPath()
		if err != nil {
			return err
		}
		fmt.Println("asking for your password…")
		if err := elevate(exe, []string{"proxy", "setup", "--system", "--ca", env.CAPath()}, "setup"); err != nil {
			return err
		}
	}
	st, err := proxyState(env, "proxy.reload", true)
	if err != nil {
		return err
	}
	fmt.Println()
	printChecks(st.Checks)
	return nil
}

// admin tells whether plan has a change that needs the password, and can be
// done.
func admin(plan []devurl.Change) bool {
	for _, c := range plan {
		if c.Admin && c.Blocked == "" {
			return true
		}
	}
	return false
}

// selfPath is this omaterm, as the elevated step is asked to run it: pkexec
// takes a full path, and a link resolved so that it is the program asked.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// elevate runs omaterm's own system step as root, and says what to run in a
// terminal when there is no polkit agent to ask for the password.
func elevate(exe string, args []string, what string) error {
	err := devurl.Elevate(runtime.GOOS, exe, args, os.Stdout, os.Stderr)
	if errors.Is(err, devurl.ErrNoAgent) {
		return fmt.Errorf("%w: run this in a terminal, then omaterm proxy status:\n  sudo %s", err, strings.Join(append([]string{exe}, args...), " "))
	}
	if err != nil {
		return fmt.Errorf("the %s step: %w", what, err)
	}
	return nil
}

func proxyUninstall(env devurl.Env, args []string) error {
	f, err := parse(args, map[string]string{"ca": "ca"}, map[string]string{"system": "system"})
	if err != nil {
		return err
	}
	if f.set["system"] {
		if f.vals["ca"] == "" {
			return usageErr("omaterm proxy uninstall --system --ca <ca.pem>")
		}
		return devurl.RemoveSystem(env, f.vals["ca"])
	}
	if env.OS != "linux" && env.OS != "darwin" {
		return devurl.ErrUnsupported
	}
	if err := devurl.RemoveUser(env); err != nil {
		return err
	}
	if devurl.SystemInstalled(env) {
		exe, err := selfPath()
		if err != nil {
			return err
		}
		fmt.Println("asking for your password…")
		// The authority's files stay if this fails: some systems remove
		// it from their store by its file.
		if err := elevate(exe, []string{"proxy", "uninstall", "--system", "--ca", env.CAPath()}, "uninstall"); err != nil {
			return err
		}
	}
	if err := devurl.RemoveFiles(env); err != nil {
		return err
	}
	fmt.Println("✓ the certificate authority is removed")
	fmt.Println("the proxy serves with the old one until omaterm restarts: omaterm daemon stop")
	return nil
}

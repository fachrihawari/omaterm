package main

// Dev URLs in the Settings window: what the proxy needs and whether it is
// so, the one-time setup that makes a browser trust it, and the routes it
// serves. The omaterm proxy command does the same; both use internal/devurl.

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/devurl"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
)

// devurlData is the page's: the proxy as the session daemon last told, the
// plan setup would run, and how a setup or removal in the background is
// going.
type devurlData struct {
	known, fetching bool
	status          daemon.ProxyStatus
	plan            []devurl.Change
	planErr         string
	// running names the work in the background ("setup" or "remove"); msg
	// is what to tell of it or of its end, failed that it went wrong.
	running string
	msg     string
	failed  bool
	// terminal is the command to run in a terminal, when no password
	// dialog could open.
	terminal []string
}

// devurlOps is what the page does through the outside world, which the
// tests replace: the daemon, the system and the password dialog.
type devurlOps struct {
	available func(a *appState) bool
	status    func() (daemon.ProxyStatus, error)
	reload    func() (daemon.ProxyStatus, error)
	plan      func() ([]devurl.Change, error)
	env       func() devurl.Env
	exe       func() (string, error)
	elevate   func(exe string, args []string) error
}

var devurlTools devurlOps

func init() { devurlTools = realDevurlOps() }

func realDevurlOps() devurlOps {
	return devurlOps{
		available: (*appState).webAvailable,
		status:    func() (daemon.ProxyStatus, error) { return askProxy("proxy.status") },
		reload:    func() (daemon.ProxyStatus, error) { return askProxy("proxy.reload") },
		plan:      func() ([]devurl.Change, error) { return devurl.Plan(devurlTools.env()) },
		env: func() devurl.Env {
			e := devurl.DefaultEnv(filepath.Join(paths.StateDir(), "devurl"))
			e.Out = nil // the page tells the progress, not a terminal
			return e
		},
		exe: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(exe)
		},
		elevate: func(exe string, args []string) error {
			var errText bytes.Buffer
			err := devurl.Elevate(runtime.GOOS, exe, args, io.Discard, &errText)
			if err != nil && !errors.Is(err, devurl.ErrNoAgent) && !errors.Is(err, devurl.ErrCancelled) {
				if msg := strings.TrimSpace(errText.String()); msg != "" {
					return errors.New(msg)
				}
			}
			return err
		},
	}
}

// askProxy asks the session daemon, which starts when it isn't running: the
// page is only drawn while it is the one that runs the panes.
func askProxy(method string) (daemon.ProxyStatus, error) {
	st, err := daemon.CallProxy(method, false)
	if errors.Is(err, client.ErrUnreachable) {
		return daemon.ProxyStatus{}, errors.New("the session daemon isn't running")
	}
	return st, err
}

// fetchDevurls reads the proxy's status and the setup plan in the
// background; the page shows them when they come.
func (a *appState) fetchDevurls() {
	d := &a.settingsUI.data.devurls
	if d.fetching {
		return
	}
	d.fetching = true
	go func() {
		st, err := devurlTools.status()
		plan, planErr := devurlTools.plan()
		a.post(func() {
			d := &a.settingsUI.data.devurls
			d.fetching, d.known = false, true
			if err != nil {
				st = daemon.ProxyStatus{Error: err.Error()}
			}
			d.status, d.plan, d.planErr = st, plan, ""
			if planErr != nil {
				d.planErr = planErr.Error()
			}
			a.invalidateWindows()
		})
	}()
}

// allChecksOK tells whether every check passes.
func (d *devurlData) allChecksOK() bool {
	for _, c := range d.status.Checks {
		if !c.OK {
			return false
		}
	}
	return len(d.status.Checks) > 0
}

// made tells whether the authority exists, so that there is something to
// remove.
func (d *devurlData) made() bool {
	for _, c := range d.status.Checks {
		if c.Name == "Certificate authority made" {
			return c.OK
		}
	}
	return false
}

// link is the address a route is served at: the proxy's port is left out
// when it is 443.
func devurlLink(host, addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "443" {
		host = net.JoinHostPort(host, port)
	}
	return "https://" + host
}

func (a *appState) settingsDevurls(c *ui.Context) {
	d := &a.settingsUI.data.devurls
	if onWindows {
		a.settingsNotOnWindows(c, "devurlSetup", "Dev URLs aren't available on Windows yet", "")
		return
	}
	if !devurlTools.available(a) {
		a.settingsGroup(c, "devurlSetup", "", "", func() {
			a.settingsRow(c, "Needs the session daemon", settingsRowOpts{id: "devurlSetup",
				sub: "The proxy runs in the session daemon, which is off. Turn on “Keep sessions running when the window closes” in General."}, nil)
		})
		return
	}
	if !d.known {
		a.fetchDevurls()
	}
	a.settingsGroup(c, "devurlSetup", "Setup", "", func() {
		switch {
		case !d.known:
			a.settingsRow(c, "Checking…", settingsRowOpts{}, nil)
		case len(d.status.Checks) == 0:
			a.settingsRow(c, "Could not check", settingsRowOpts{sub: d.status.Error}, nil)
		}
		for _, ck := range d.status.Checks {
			mark := -1
			if ck.OK {
				mark = 1
			}
			a.settingsRow(c, ck.Name, settingsRowOpts{id: "devurl-" + ck.Name, check: mark, sub: ck.Detail}, nil)
		}
	})
	if d.msg != "" {
		col := pal.TextMuted
		if d.failed {
			col = pal.Bad
		}
		ui.Text(c.Key("devurlMsg"), d.msg).FontSize(11.5).TextColor(col).Margin(6, 2, 0, 2).Wrap()
	}
	a.devurlActions(c)
	a.settingsGroup(c, "devurlRoutes", "Routes", "", func() {
		if len(d.status.Routes) == 0 {
			a.settingsRow(c, "No process has a URL yet", settingsRowOpts{
				sub: "Give a process a URL in its Edit dialog: url: shop serves it at https://shop.localhost."}, nil)
		}
		for _, rt := range d.status.Routes {
			link := devurlLink(rt.Host, d.status.Addr)
			a.settingsRow(c, rt.Process+" · "+rt.Project, settingsRowOpts{id: "route-" + rt.Host, sub: link, mono: true}, func() {
				if rt.Running {
					settingsPill(c, "Running", pillOk)
				} else {
					status := rt.Status
					if status == "" {
						status = "stopped"
					}
					settingsPill(c, strings.ToUpper(status[:1])+status[1:], pillPlain)
				}
				if a.settingsButton(c, "Open##"+rt.Host, false, false, !d.status.Running) {
					a.openURL(link)
				}
				if a.settingsButton(c, "Copy##"+rt.Host, false, false, false) {
					mygo.Clipboard.WriteText(link)
					toast(c, "Copied "+link)
				}
			})
		}
	})
}

// devurlActions are the buttons under the checks: Set Up where something is
// missing, Remove where something is set up.
func (a *appState) devurlActions(c *ui.Context) {
	d := &a.settingsUI.data.devurls
	busy := d.running != ""
	settingsActions(c, func() {
		if len(d.terminal) > 0 {
			if a.settingsButton(c, "Run in a Terminal", false, false, busy) {
				a.devurlTerminal()
			}
		}
		if d.known && d.made() {
			if a.settingsButton(c, "Remove…", false, true, busy) {
				a.devurlRemoveSheet()
			}
		}
		switch {
		case !d.known:
		case len(d.plan) > 0:
			label := "Set Up…"
			if busy && d.running == "setup" {
				label = "Setting Up…"
			}
			if a.settingsButton(c, label, true, false, busy) {
				a.devurlSetupSheet()
			}
		case !d.status.Running:
			if a.settingsButton(c, "Try Again", true, false, busy) {
				a.devurlReload()
			}
		}
	})
}

// devurlSetupSheet lists what setting up changes, and which of it asks for
// the password, before it does any.
func (a *appState) devurlSetupSheet() {
	d := &a.settingsUI.data.devurls
	plan := append([]devurl.Change(nil), d.plan...)
	sh := &settingsSheet{title: "Set Up Dev URLs?", text: "These are the changes, in this order."}
	runnable := false
	for _, ch := range plan {
		note := ""
		switch {
		case ch.Blocked != "":
			note = "Can’t be done here: " + ch.Blocked
		case ch.Admin:
			note = "Asks for your password"
			runnable = true
		default:
			runnable = true
		}
		sh.items = append(sh.items, [2]string{ch.Text, note})
	}
	if runnable {
		sh.confirm = "Set Up"
		sh.run = func(*ui.Context) { a.devurlSetup(plan) }
	}
	a.settingsUI.sheet = sh
}

// devurlSetup runs the plan in the background: the user's part, the system
// part through the desktop's password dialog, then the daemon binds again.
func (a *appState) devurlSetup(plan []devurl.Change) {
	d := &a.settingsUI.data.devurls
	d.running, d.msg, d.failed, d.terminal = "setup", "Setting up…", false, nil
	go func() {
		env := devurlTools.env()
		msg, failed, terminal := "", false, []string(nil)
		if err := devurl.ApplyUser(env, plan); err != nil {
			msg, failed = err.Error(), true
		}
		if msg == "" && adminPending(plan) {
			a.post(func() {
				a.settingsUI.data.devurls.msg = "Asking for your password…"
				a.invalidateWindows()
			})
			exe, err := devurlTools.exe()
			args := []string{"proxy", "setup", "--system", "--ca", env.CAPath()}
			if err == nil {
				err = devurlTools.elevate(exe, args)
			}
			switch {
			case errors.Is(err, devurl.ErrNoAgent):
				msg, failed = "No password dialog could open: run the step in a terminal, then press Try Again.", true
				terminal = append([]string{"sudo", exe}, args...)
			case errors.Is(err, devurl.ErrCancelled):
				msg, failed = "Cancelled: no password was given.", true
			case err != nil:
				msg, failed = "The system step failed: "+err.Error(), true
			}
		}
		var st daemon.ProxyStatus
		var rerr error
		if !failed {
			st, rerr = devurlTools.reload()
			if rerr != nil {
				msg, failed = "The proxy did not restart: "+rerr.Error(), true
			} else {
				msg = "Dev URLs is set up."
			}
		}
		a.post(func() { a.devurlDone(msg, failed, terminal, st, rerr == nil && !failed) })
	}()
}

// adminPending tells whether plan has a change that needs the password and
// can be done.
func adminPending(plan []devurl.Change) bool {
	for _, ch := range plan {
		if ch.Admin && ch.Blocked == "" {
			return true
		}
	}
	return false
}

// devurlDone ends a setup or a removal on the page: its message, and, when
// the daemon answered, the proxy's new state; the page reads the rest again.
func (a *appState) devurlDone(msg string, failed bool, terminal []string, st daemon.ProxyStatus, gotStatus bool) {
	d := &a.settingsUI.data.devurls
	d.running, d.msg, d.failed, d.terminal = "", msg, failed, terminal
	if gotStatus {
		d.status = st
	}
	a.settingsUI.dataAt = time.Time{}
	a.fetchDevurls()
	a.settingsToast(msg)
	a.invalidateWindows()
}

// devurlReload asks the daemon to bind the proxy again, as after the system
// step ran in a terminal.
func (a *appState) devurlReload() {
	d := &a.settingsUI.data.devurls
	d.running, d.msg, d.failed = "reload", "Starting the proxy…", false
	go func() {
		st, err := devurlTools.reload()
		msg, failed := "The proxy is running.", false
		if err != nil {
			msg, failed = "The proxy did not start: "+err.Error(), true
		} else if !st.Running {
			msg, failed = "The proxy did not start: "+st.Error, true
		}
		a.post(func() { a.devurlDone(msg, failed, nil, st, err == nil) })
	}()
}

// devurlTerminal opens a session in the main window that runs the system
// step with sudo, for a machine with no password dialog.
func (a *appState) devurlTerminal() {
	d := &a.settingsUI.data.devurls
	script := `"$@"; status=$?; echo; printf 'Press Enter to close. '; read -r _; exit $status`
	cmd := append([]string{"sh", "-c", script, "sh"}, d.terminal...)
	if _, err := a.cliCreate(proto.CreateParams{Name: "Dev URLs setup", Command: cmd, Focus: true}); err != nil {
		a.settingsToast("Could not open a terminal: " + err.Error())
		return
	}
	d.msg, d.failed = "A terminal is open in omaterm’s window. Once it finishes, press Try Again.", false
	a.settingsToast("Opened a terminal in omaterm’s window")
}

// devurlRemoveSheet asks before taking Dev URLs away.
func (a *appState) devurlRemoveSheet() {
	a.settingsUI.sheet = &settingsSheet{
		title: "Remove Dev URLs?", text: "The URLs stop working until you set it up again.", confirm: "Remove", danger: true,
		items: devurlRemoveItems(runtime.GOOS),
		run:   func(*ui.Context) { a.devurlRemove() },
	}
}

// devurlRemoveItems are what removing takes away on goos. On macOS only
// Firefox keeps its own trust (Chrome and Safari read the System keychain),
// and there is no port setting: that is Linux's sysctl.
func devurlRemoveItems(goos string) [][2]string {
	browsers, system := "Trust in Chrome, Chromium and Firefox", "Trust system-wide, and the setting that lets programs use port 443"
	if goos == "darwin" {
		browsers, system = "Trust in Firefox", "Trust in the System keychain"
	}
	return [][2]string{
		{browsers, ""},
		{system, "Asks for your password"},
		{"omaterm’s certificate authority and its key", ""},
	}
}

// devurlRemove undoes the setup in the background, as omaterm proxy
// uninstall does: the authority's files stay when the system part fails.
func (a *appState) devurlRemove() {
	d := &a.settingsUI.data.devurls
	d.running, d.msg, d.failed, d.terminal = "remove", "Removing…", false, nil
	go func() {
		env := devurlTools.env()
		msg, failed := "Dev URLs is removed. The proxy serves with its old authority until omaterm restarts.", false
		terminal := []string(nil)
		err := devurl.RemoveUser(env)
		if err == nil && devurl.SystemInstalled(env) {
			var exe string
			args := []string{"proxy", "uninstall", "--system", "--ca", env.CAPath()}
			if exe, err = devurlTools.exe(); err == nil {
				err = devurlTools.elevate(exe, args)
			}
			if errors.Is(err, devurl.ErrNoAgent) {
				terminal = append([]string{"sudo", exe}, args...)
			}
		}
		if err == nil {
			err = devurl.RemoveFiles(env)
		}
		switch {
		case errors.Is(err, devurl.ErrNoAgent):
			msg, failed = "No password dialog could open: run the step in a terminal, then remove again.", true
		case errors.Is(err, devurl.ErrCancelled):
			msg, failed = "Cancelled: no password was given.", true
		case err != nil:
			msg, failed = "Could not remove it: "+err.Error(), true
		}
		a.post(func() { a.devurlDone(msg, failed, terminal, daemon.ProxyStatus{}, false) })
	}()
}

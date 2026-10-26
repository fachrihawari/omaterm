//go:build e2e

// Package e2e runs the real omaterm in a headless sway of its own, apart
// from the developer's desktop: its own Wayland display, D-Bus, home,
// config, state and runtime folders. Tests drive it with the omaterm
// command and with keys (wtype), look at its windows (swaymsg) and take
// screenshots (grim). Run with scripts/e2e.sh, or:
//
//	OMATERM_E2E=1 go test -tags e2e -count=1 -v ./e2e/
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// world is the headless desktop every test shares.
type world struct {
	root     string // the run's temporary folder
	bin      string // the omaterm built for the run
	runtime  string // sway's runtime folder, where its sockets are
	wayland  string // the Wayland socket, absolute
	swaysock string
	bus      string // the private D-Bus session's address
	shots    string // where screenshots go
	procs    []*exec.Cmd
}

var w *world

func TestMain(m *testing.M) {
	if os.Getenv("OMATERM_E2E") != "1" {
		fmt.Println("e2e: skipped (set OMATERM_E2E=1)")
		os.Exit(0)
	}
	var err error
	w, err = setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		if w != nil {
			w.teardown()
		}
		os.Exit(1)
	}
	code := m.Run()
	w.teardown()
	os.Exit(code)
}

func setup() (*world, error) {
	root, err := os.MkdirTemp("", "omaterm-e2e-")
	if err != nil {
		return nil, err
	}
	w := &world{root: root, runtime: filepath.Join(root, "run")}
	w.shots = os.Getenv("OMATERM_E2E_SHOTS")
	if w.shots == "" {
		w.shots = filepath.Join(root, "shots")
	}
	for _, d := range []string{w.runtime, w.shots} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return w, err
		}
	}
	w.bin = filepath.Join(root, "omaterm")
	build := exec.Command("go", "build", "-o", w.bin, "..")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		return w, fmt.Errorf("go build: %w", err)
	}

	// A D-Bus session of its own: notifications and portals must not reach
	// the developer's desktop.
	busPath := filepath.Join(root, "bus")
	w.bus = "unix:path=" + busPath
	dbus := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+w.bus)
	if err := dbus.Start(); err != nil {
		return w, fmt.Errorf("dbus-daemon: %w", err)
	}
	w.procs = append(w.procs, dbus)

	cfg := filepath.Join(root, "sway.conf")
	os.WriteFile(cfg, []byte("xwayland disable\noutput * resolution 1440x900\ndefault_border none\nfocus_on_window_activation focus\n"), 0o600)
	sway := exec.Command("sway", "-c", cfg)
	sway.Env = cleanEnv(map[string]string{
		"XDG_RUNTIME_DIR":          w.runtime,
		"WLR_BACKENDS":             "headless",
		"WLR_LIBINPUT_NO_DEVICES":  "1",
		"WLR_RENDERER":             "pixman",
		"DBUS_SESSION_BUS_ADDRESS": w.bus,
	})
	logf, _ := os.Create(filepath.Join(root, "sway.log"))
	sway.Stdout, sway.Stderr = logf, logf
	if err := sway.Start(); err != nil {
		return w, fmt.Errorf("sway: %w", err)
	}
	w.procs = append(w.procs, sway)
	if !poll(10*time.Second, func() bool {
		socks, _ := filepath.Glob(filepath.Join(w.runtime, "wayland-*"))
		ipc, _ := filepath.Glob(filepath.Join(w.runtime, "sway-ipc.*.sock"))
		for _, s := range socks {
			if !strings.HasSuffix(s, ".lock") {
				w.wayland = s
			}
		}
		if len(ipc) > 0 {
			w.swaysock = ipc[0]
		}
		return w.wayland != "" && w.swaysock != ""
	}) {
		return w, fmt.Errorf("sway did not start: see %s", filepath.Join(root, "sway.log"))
	}
	return w, nil
}

func (w *world) teardown() {
	for i := len(w.procs) - 1; i >= 0; i-- {
		if p := w.procs[i].Process; p != nil {
			p.Kill()
			w.procs[i].Wait()
		}
	}
	if os.Getenv("OMATERM_E2E_KEEP") == "" {
		os.RemoveAll(w.root)
	} else {
		fmt.Println("e2e: kept", w.root)
	}
}

// cleanEnv is the environment of the tests' processes: nothing of the
// developer's session (display, Hyprland, omaterm) and the given values.
func cleanEnv(set map[string]string) []string {
	drop := []string{"DISPLAY", "WAYLAND_DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE", "SWAYSOCK",
		"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "HOME"}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "OMATERM_") || contains(drop, k) {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// poll calls ok until it reports true or the time is up.
func poll(d time.Duration, ok func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return ok()
}

// App is an omaterm running in the world, with folders of its own.
type App struct {
	t    *testing.T
	dir  string
	env  []string
	cmd  *exec.Cmd
	log  *bytes.Buffer
	done chan struct{}
}

// Options are how an App starts.
type Options struct {
	// Daemon runs the session daemon; off runs the panes in the app.
	Daemon bool
	// Settings are written to settings.json before it starts.
	Settings map[string]any
	Args     []string
}

// Start runs omaterm, and waits for its control socket.
func Start(t *testing.T, o Options) *App {
	t.Helper()
	dir, err := os.MkdirTemp(w.root, strings.ReplaceAll(t.Name(), "/", "_")+"-")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{t: t, dir: dir}
	for _, d := range []string{"home", "config/omaterm", "state", "run", "project"} {
		os.MkdirAll(filepath.Join(dir, d), 0o700)
	}
	set := map[string]string{
		"HOME":                     filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME":          filepath.Join(dir, "config"),
		"XDG_STATE_HOME":           filepath.Join(dir, "state"),
		"XDG_RUNTIME_DIR":          filepath.Join(dir, "run"),
		"WAYLAND_DISPLAY":          w.wayland,
		"DBUS_SESSION_BUS_ADDRESS": w.bus,
		"GDK_BACKEND":              "wayland",
		"MYGO_GPU":                 "0",
		"OMATERM_OMARCHY_DIR":      filepath.Join(dir, "no-omarchy"),
		"SHELL":                    "/bin/sh",
	}
	if !o.Daemon {
		set["OMATERM_NO_DAEMON"] = "1"
	}
	a.env = cleanEnv(set)
	settings := map[string]any{"remoteControl": true, "daemon": o.Daemon}
	for k, v := range o.Settings {
		settings[k] = v
	}
	b, _ := json.Marshal(settings)
	os.WriteFile(filepath.Join(dir, "config", "omaterm", "settings.json"), b, 0o600)
	a.launch(o.Args...)
	t.Cleanup(a.Stop)
	return a
}

// launch starts the app process and waits until it answers.
func (a *App) launch(args ...string) {
	a.t.Helper()
	a.log = &bytes.Buffer{}
	a.cmd = exec.Command(w.bin, args...)
	a.cmd.Env = a.env
	a.cmd.Dir = filepath.Join(a.dir, "project")
	a.cmd.Stdout, a.cmd.Stderr = a.log, a.log
	if err := a.cmd.Start(); err != nil {
		a.t.Fatal(err)
	}
	a.done = make(chan struct{})
	go func(cmd *exec.Cmd, done chan struct{}) { cmd.Wait(); close(done) }(a.cmd, a.done)
	if !poll(15*time.Second, func() bool { _, err := a.CLI("ls", "--json"); return err == nil }) {
		a.t.Fatalf("omaterm did not answer; its output:\n%s", a.log)
	}
	a.WaitWindow("omaterm", true)
}

// Stop quits the app, and the daemon it started.
func (a *App) Stop() {
	if a.cmd == nil || a.cmd.Process == nil {
		return
	}
	select {
	case <-a.done:
	default:
		a.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-a.done:
		case <-time.After(5 * time.Second):
			a.cmd.Process.Kill()
			<-a.done
		}
	}
	a.CLI("daemon", "stop")
}

// CLI runs the omaterm command against this app.
func (a *App) CLI(args ...string) (string, error) {
	cmd := exec.Command(w.bin, args...)
	cmd.Env = a.env
	// Sessions it makes start in the test's own project, not this repo.
	cmd.Dir = filepath.Join(a.dir, "project")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// MustCLI is CLI that fails the test on an error.
func (a *App) MustCLI(args ...string) string {
	a.t.Helper()
	out, err := a.CLI(args...)
	if err != nil {
		a.t.Fatalf("omaterm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// Alive fails the test when the app has exited or logged a crash.
func (a *App) Alive() {
	a.t.Helper()
	select {
	case <-a.done:
		a.t.Fatalf("omaterm exited; its output:\n%s", a.log)
	default:
	}
	for _, name := range []string{"crash-app.log", "crash-daemon.log"} {
		if b, err := os.ReadFile(filepath.Join(a.dir, "state", "omaterm", name)); err == nil && len(b) > 0 {
			a.t.Fatalf("%s:\n%s", name, b)
		}
	}
	if _, err := a.CLI("ls", "--json"); err != nil {
		a.t.Fatalf("omaterm does not answer: %v", err)
	}
}

// Key types keys with wtype into the focused window, as wtype's
// arguments: Key("-k", "Escape"), Key("-M", "ctrl", "w", "-m", "ctrl").
func (a *App) Key(args ...string) {
	a.t.Helper()
	// A new virtual keyboard's first key is lost while the compositor
	// takes its keymap: wait before typing.
	cmd := exec.Command("wtype", append([]string{"-s", "150"}, args...)...)
	cmd.Env = a.env
	if out, err := cmd.CombinedOutput(); err != nil {
		a.t.Fatalf("wtype %v: %v %s", args, err, out)
	}
}

// Windows lists the titles of the app's windows on screen, and which has
// the keyboard.
func (a *App) Windows() (titles []string, focused string) {
	cmd := exec.Command("swaymsg", "-s", w.swaysock, "-t", "get_tree", "-r")
	cmd.Env = a.env
	out, err := cmd.Output()
	if err != nil {
		a.t.Fatalf("swaymsg: %v", err)
	}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if pid, ok := n["pid"].(float64); ok && int(pid) == a.cmd.Process.Pid {
			name, _ := n["name"].(string)
			titles = append(titles, name)
			if f, _ := n["focused"].(bool); f {
				focused = name
			}
		}
		for _, key := range []string{"nodes", "floating_nodes"} {
			kids, _ := n[key].([]any)
			for _, k := range kids {
				if m, ok := k.(map[string]any); ok {
					walk(m)
				}
			}
		}
	}
	var tree map[string]any
	json.Unmarshal(out, &tree)
	walk(tree)
	return titles, focused
}

// WaitWindow waits until a window of the title is on screen, or gone.
func (a *App) WaitWindow(title string, shown bool) {
	a.t.Helper()
	if !poll(10*time.Second, func() bool {
		titles, _ := a.Windows()
		return contains(titles, title) == shown
	}) {
		titles, _ := a.Windows()
		a.t.Fatalf("window %q shown=%v never happened; windows: %v", title, !shown, titles)
	}
}

// WaitFocus waits until the window of the title has the keyboard.
func (a *App) WaitFocus(title string) {
	a.t.Helper()
	if !poll(5*time.Second, func() bool { _, f := a.Windows(); return f == title }) {
		_, f := a.Windows()
		a.t.Fatalf("window %q never took the keyboard (focused: %q)", title, f)
	}
}

// Shot saves a screenshot of the screen, named for the test.
func (a *App) Shot(name string) {
	path := filepath.Join(w.shots, strings.ReplaceAll(a.t.Name(), "/", "_")+"-"+name+".png")
	cmd := exec.Command("grim", path)
	cmd.Env = a.env
	if out, err := cmd.CombinedOutput(); err != nil {
		a.t.Logf("grim: %v %s", err, out)
	}
}

// Sway runs a sway command, such as one that focuses a window.
func (a *App) Sway(command string) {
	a.t.Helper()
	cmd := exec.Command("swaymsg", "-s", w.swaysock, command)
	cmd.Env = a.env
	if out, err := cmd.CombinedOutput(); err != nil {
		a.t.Fatalf("swaymsg %s: %v %s", command, err, out)
	}
}

// WindowShot is a PNG of the window of the title, as it shows now.
func (a *App) WindowShot(title string) []byte {
	a.t.Helper()
	cmd := exec.Command("swaymsg", "-s", w.swaysock, "-t", "get_tree", "-r")
	cmd.Env = a.env
	out, err := cmd.Output()
	if err != nil {
		a.t.Fatalf("swaymsg: %v", err)
	}
	type node struct {
		Name     string `json:"name"`
		Rect     struct{ X, Y, Width, Height int }
		Nodes    []node `json:"nodes"`
		Floating []node `json:"floating_nodes"`
	}
	var find func(n node) *node
	find = func(n node) *node {
		if n.Name == title && n.Rect.Width > 0 {
			return &n
		}
		for _, k := range append(n.Nodes, n.Floating...) {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	var tree node
	json.Unmarshal(out, &tree)
	n := find(tree)
	if n == nil {
		a.t.Fatalf("no window %q to shoot", title)
	}
	r := n.Rect
	cmd = exec.Command("grim", "-g", fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.Width, r.Height), "-")
	cmd.Env = a.env
	png, err := cmd.Output()
	if err != nil {
		a.t.Fatalf("grim: %v", err)
	}
	return png
}

// Relaunch quits the app and starts it again on the same folders.
func (a *App) Relaunch(args ...string) {
	a.t.Helper()
	a.Quit()
	a.launch(args...)
}

// Quit quits the app as the desktop would (SIGTERM, which quits as the
// window's close does), and waits for it.
func (a *App) Quit() {
	a.t.Helper()
	a.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		a.t.Fatalf("omaterm did not quit; its output:\n%s", a.log)
	}
}

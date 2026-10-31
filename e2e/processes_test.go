//go:build e2e

package e2e

// A project's processes over the daemon, with the built binary: the file
// that defines them, the trust a command needs before it runs, the
// scrollback a restart carries over, a failure that restarts, and a
// definition that changes and has to be trusted again. This is the feature end to
// end, as a user meets it, and it needs no window: the daemon serves it.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/procs"
)

// procsYML is the project's processes: one that stays up, and one that
// fails at once and is restarted.
const procsYML = `processes:
  web:
    command: sh -c 'echo up; exec sleep 300'
    port: 0
  bad:
    command: sh -c 'echo boom; exit 1'
    restart: on-failure
`

func TestProjectProcessesOverTheDaemon(t *testing.T) {
	a := Start(t, Options{Daemon: true})
	// The daemon serves the processes; the app is only where they show.
	a.Quit()

	// The folder a command runs in, which a project command looks up: the
	// harness runs every CLI call here.
	project := filepath.Join(a.dir, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(project, procs.FileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(procsYML)
	a.addProject("p1", "proj", project)
	a.reload()

	ps := func() []procs.Proc { return a.ps(t) }
	if got := len(ps()); got != 2 {
		t.Fatalf("ps lists %d processes, want 2", got)
	}
	for _, p := range ps() {
		if p.Trusted {
			t.Fatalf("%s is trusted before the user trusted it", p.Name)
		}
	}

	// 2. A command from the file runs only once its definition is trusted.
	out, err := a.CLI("start", "web")
	if err == nil || !strings.Contains(out, "isn't trusted yet") {
		t.Fatalf("an untrusted start: err=%v out=%q", err, out)
	}

	// 3. Trust the definition the file has, and the process is startable.
	a.trust(project, procs.Def{Name: "web", Command: "sh -c 'echo up; exec sleep 300'"})
	a.reload()
	if p := a.proc(t, "web"); !p.Trusted || p.Status == procs.Untrusted {
		t.Fatalf("after trust: %+v", p)
	}

	// 4. It runs, and says so in its pane.
	a.MustCLI("start", "web", "--wait")
	if p := a.proc(t, "web"); p.Status != procs.Running {
		t.Fatalf("after start: %+v", p)
	}
	if got := a.MustCLI("read", "web"); !strings.Contains(got, "up") {
		t.Fatalf("the pane's screen: %q", got)
	}
	if ls := a.MustCLI("ls"); !strings.Contains(ls, "web") {
		t.Fatalf("ls has no web session:\n%s", ls)
	}

	// 5. A restart says why in the scrollback, after what the run printed.
	a.MustCLI("restart", "web", "--wait")
	if got := a.MustCLI("read", "web", "-a"); !strings.Contains(got, "── restarted ──") {
		t.Fatalf("the restarted pane's scrollback: %q", got)
	}

	// 6. A stopped process is stopped, and its pane goes with the run.
	a.MustCLI("stop", "web")
	if p := a.proc(t, "web"); p.Status != procs.Stopped {
		t.Fatalf("after stop: %+v", p)
	}
	// The pane is let go with the run it belonged to: a stopped process
	// has no pane to read, and the daemon keeps no scrollback for one,
	// as TestNoHistoryAfterClose in internal/daemon insists.
	if out, err := a.CLI("read", "web"); err == nil {
		t.Fatalf("a stopped process still has a pane: %q", out)
	}

	// 7. A failure restarts, with the exit code that caused it.
	a.trust(project, procs.Def{Name: "bad", Command: "sh -c 'echo boom; exit 1'", Restart: "on-failure"})
	a.reload()
	a.MustCLI("start", "bad")
	if !poll(5*time.Second, func() bool {
		p := a.proc(t, "bad")
		return p.Restarts >= 1 && p.LastExit == 1
	}) {
		t.Fatalf("the failing process never restarted: %+v", a.proc(t, "bad"))
	}

	// 8. The daemon keeps them across a restart, and starts nothing by
	// itself: no definition asks for it.
	a.MustCLI("daemon", "stop")
	a.restartDaemon()
	names := map[string]bool{}
	for _, p := range ps() {
		names[p.Name] = true
		if p.Status != procs.Stopped {
			t.Fatalf("after the daemon restarted, %s is %s", p.Name, p.Status)
		}
	}
	if !names["web"] || !names["bad"] {
		t.Fatalf("the processes are gone after a daemon restart: %v", names)
	}

	// 9. A changed command is a definition the user has not seen.
	write(strings.Replace(procsYML, "echo up", "echo up again", 1))
	if !poll(5*time.Second, func() bool {
		return !a.proc(t, "web").Trusted
	}) {
		t.Fatalf("a changed definition stayed trusted: %+v", a.proc(t, "web"))
	}
	if out, err := a.CLI("start", "web"); err == nil || !strings.Contains(out, "isn't trusted yet") {
		t.Fatalf("a changed definition still runs: err=%v out=%q", err, out)
	}
}

// addProject adds a project to the daemon's state, before it reads it: the
// app is closed, and the daemon loads the file on its next call.
func (a *App) addProject(id, name, dir string) {
	a.t.Helper()
	path := filepath.Join(a.dir, "config", "omaterm", "state.json")
	var st model.State
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &st); err != nil {
			a.t.Fatal(err)
		}
	}
	st.Version = model.Version
	st.ActiveProject = id
	st.Projects = append(st.Projects, model.Project{ID: id, Name: name, Dir: dir})
	b, err := json.Marshal(st)
	if err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		a.t.Fatal(err)
	}
}

// trust records the user's trust in a definition, as reviewing it in
// omaterm does: the variant is the hash of what the process runs.
func (a *App) trust(dir string, def procs.Def) {
	a.t.Helper()
	path := filepath.Join(a.dir, "config", "omaterm", "trust.json")
	trust := procs.OpenTrust(path)
	if err := trust.Add(dir, def.Variant()); err != nil {
		a.t.Fatal(err)
	}
}

// ps is the running project's processes.
func (a *App) ps(t *testing.T) []procs.Proc {
	t.Helper()
	out := a.MustCLI("ps", "--json")
	var list []procs.Proc
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("ps --json: %v\n%s", err, out)
	}
	return list
}

// proc is one of the running project's processes.
func (a *App) proc(t *testing.T, name string) procs.Proc {
	t.Helper()
	for _, p := range a.ps(t) {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no process %q: %s", name, a.MustCLI("ps"))
	return procs.Proc{}
}

// reload makes the daemon read the project's files again, as it does when
// it starts. Its projects, their omaterm.yml and their trust are read then:
// a file written by hand while it runs is not what it watches.
func (a *App) reload() {
	a.t.Helper()
	a.MustCLI("daemon", "stop")
	a.restartDaemon()
}

// restartDaemon starts the daemon again after `daemon stop`, as the next
// command that needs one would, and waits for it to answer.
func (a *App) restartDaemon() {
	a.t.Helper()
	cmd := exec.Command(w.bin, "daemon", "serve")
	cmd.Env = a.env
	cmd.Dir = filepath.Join(a.dir, "project")
	log, err := os.Create(filepath.Join(a.dir, "daemon.log"))
	if err != nil {
		a.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	// Its own process group: the test stops the daemon by its pid, and a
	// shell that started it must not take it with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
		log.Close()
	})
	if !poll(15*time.Second, func() bool { _, err := a.CLI("ps", "--json"); return err == nil }) {
		a.t.Fatalf("the daemon did not come back; its output:\n%s", readFile(filepath.Join(a.dir, "daemon.log")))
	}
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/devurl"
	"omaterm/internal/theme"
)

// fakeDevurl replaces what the page does through the world: the daemon, the
// system and the password dialog. It records what was asked.
type fakeDevurl struct {
	status   daemon.ProxyStatus
	plan     []devurl.Change
	env      devurl.Env
	elevated [][]string
	elevErr  error
	reloads  int
}

func useFakeDevurl(t *testing.T, daemonUp bool) *fakeDevurl {
	t.Helper()
	root := t.TempDir()
	f := &fakeDevurl{env: devurl.Env{OS: "linux", Home: filepath.Join(root, "home"), Root: filepath.Join(root, "sys"),
		CADir: filepath.Join(root, "home", "state", "devurl"), Geteuid: func() int { return 1000 }}}
	old := devurlTools
	devurlTools = devurlOps{
		available: func(*appState) bool { return daemonUp },
		status:    func() (daemon.ProxyStatus, error) { return f.status, nil },
		reload: func() (daemon.ProxyStatus, error) {
			f.reloads++
			f.status.Running = true
			return f.status, nil
		},
		plan: func() ([]devurl.Change, error) { return f.plan, nil },
		env:  func() devurl.Env { return f.env },
		exe:  func() (string, error) { return "/opt/omaterm", nil },
		elevate: func(exe string, args []string) error {
			f.elevated = append(f.elevated, append([]string{exe}, args...))
			return f.elevErr
		},
	}
	t.Cleanup(func() { devurlTools = old })
	return f
}

var (
	someChecks = []devurl.Check{
		{Name: "Certificate authority made", OK: true, Detail: "~/.local/state/omaterm/devurl/ca.pem"},
		{Name: "Proxy serving", Detail: "listen tcp 127.0.0.1:443: permission denied"},
		{Name: "Trusted by the system", Detail: "not in the system's store"},
		{Name: "*.localhost names resolve", OK: true},
	}
	someRoutes = []devurl.Route{
		{Host: "shop.localhost", Project: "store", Process: "web", Port: 4001, Running: true, Status: "running"},
		{Host: "api.localhost", Project: "store", Process: "api", Status: "stopped"},
	}
	somePlan = []devurl.Change{
		{ID: "nss:/h/.pki/nssdb", Text: "Trust it in Chrome and Chromium (~/.pki/nssdb)"},
		{ID: "trust", Admin: true, Text: "Trust it system-wide (trust anchor)"},
		{ID: "sysctl", Admin: true, Text: "Let programs listen on ports 443 and up"},
	}
)

// devurlsPage opens Settings on the page with the status as the fake tells.
func devurlsPage(t *testing.T, f *fakeDevurl) (*appState, *ui.Tester) {
	t.Helper()
	a, tr := newSettingsTester(t, "devurls")
	a.settingsUI.data.devurls = devurlData{known: true, status: f.status, plan: f.plan}
	tr.Frame()
	t.Cleanup(func() { stopTermPanes(a) })
	return a, tr
}

// waitDevurl runs the work posted to the window until the page is idle.
func waitDevurl(t *testing.T, a *appState) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		a.drainPosted()
		if d := a.settingsUI.data.devurls; d.running == "" && !d.fetching {
			return
		}
	}
	t.Fatal("the work in the background didn't finish")
}

func TestDevurlsPageShowsChecksAndRoutes(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Running: true, Addr: "127.0.0.1:8443", Checks: someChecks, Routes: someRoutes}
	f.plan = somePlan
	_, tr := devurlsPage(t, f)
	for _, want := range []string{"Dev URLs", "Certificate authority made", "Proxy serving", "listen tcp 127.0.0.1:443: permission denied",
		"Trusted by the system", "not in the system's store", "https://shop.localhost:8443", "https://api.localhost:8443",
		"web · store", "api · store", "Running", "Stopped", "Open", "Copy", "Set Up…", "Remove…"} {
		if !tr.HasText(want) {
			t.Errorf("no %q on the page: %v", want, tr.Texts())
		}
	}
	if tr.HasText("No process has a URL yet") {
		t.Error("the empty state shows with routes")
	}
}

func TestDevurlsRoutesEmptyState(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Running: true, Addr: "127.0.0.1:443", Checks: someChecks[:1]}
	_, tr := devurlsPage(t, f)
	if !tr.HasText("No process has a URL yet") || !tr.HasText("url: shop serves it at https://shop.localhost.") {
		t.Fatalf("no empty state: %v", tr.Texts())
	}
}

func TestDevurlsLinkLeavesOutPort443(t *testing.T) {
	for addr, want := range map[string]string{"127.0.0.1:443": "https://shop.localhost", "127.0.0.1:8443": "https://shop.localhost:8443", "": "https://shop.localhost"} {
		if got := devurlLink("shop.localhost", addr); got != want {
			t.Errorf("devurlLink(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestDevurlsSetUpOpensTheSheetWithThePlan(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: someChecks}
	f.plan = append(append([]devurl.Change(nil), somePlan...), devurl.Change{ID: "nss:/h/ff", Text: "Trust it in Firefox", Blocked: "certutil isn't installed"})
	a, tr := devurlsPage(t, f)
	if err := tr.Click("Set Up…"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	sh := a.settingsUI.sheet
	if sh == nil || sh.danger || sh.confirm != "Set Up" || sh.run == nil {
		t.Fatalf("sheet %+v", sh)
	}
	var texts, notes []string
	for _, it := range sh.items {
		texts, notes = append(texts, it[0]), append(notes, it[1])
	}
	want := []string{"Trust it in Chrome and Chromium (~/.pki/nssdb)", "Trust it system-wide (trust anchor)", "Let programs listen on ports 443 and up", "Trust it in Firefox"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("items %v", texts)
	}
	if notes[0] != "" || notes[1] != "Asks for your password" || notes[2] != "Asks for your password" || !strings.Contains(notes[3], "certutil isn't installed") {
		t.Errorf("notes %v", notes)
	}
	if !tr.HasText("Set Up") || !tr.HasText("Asks for your password") {
		t.Errorf("the sheet isn't drawn: %v", tr.Texts())
	}
	if len(f.elevated) != 0 {
		t.Error("the password was asked before the user confirmed")
	}
}

func TestDevurlsSheetOfOnlyBlockedChangesCannotRun(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: someChecks}
	f.plan = []devurl.Change{{ID: "trust", Admin: true, Text: "Trust it system-wide", Blocked: "no known folder"}}
	a, _ := devurlsPage(t, f)
	a.devurlSetupSheet()
	if sh := a.settingsUI.sheet; sh == nil || sh.run != nil {
		t.Fatalf("sheet %+v", sh)
	}
}

func TestDevurlsSetUpRunsTheUserPartThenThePasswordStepThenReloads(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: someChecks}
	f.plan = []devurl.Change{{ID: "trust", Admin: true, Text: "Trust it system-wide"}}
	a, _ := devurlsPage(t, f)
	a.devurlSetup(f.plan)
	if a.settingsUI.data.devurls.running != "setup" {
		t.Fatal("not marked as running")
	}
	waitDevurl(t, a)
	want := [][]string{{"/opt/omaterm", "proxy", "setup", "--system", "--ca", f.env.CAPath()}}
	if !reflect.DeepEqual(f.elevated, want) {
		t.Errorf("elevated %v, want %v", f.elevated, want)
	}
	d := a.settingsUI.data.devurls
	if f.reloads != 1 || d.failed || d.msg != "Dev URLs is set up." || !d.status.Running {
		t.Errorf("reloads %d, %+v", f.reloads, d)
	}
}

func TestDevurlsSetUpWithTheUserPartOnlyAsksNoPassword(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.plan = []devurl.Change{{ID: "ca", Text: "Make omaterm's certificate authority"}}
	a, _ := devurlsPage(t, f)
	a.devurlSetup(f.plan)
	waitDevurl(t, a)
	if len(f.elevated) != 0 {
		t.Errorf("the password was asked for the user's part: %v", f.elevated)
	}
	if _, err := os.Stat(f.env.CAPath()); err != nil {
		t.Errorf("the authority wasn't made: %v", err)
	}
}

func TestDevurlsNoPolkitAgentOffersATerminal(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: someChecks}
	f.plan = []devurl.Change{{ID: "trust", Admin: true, Text: "Trust it system-wide"}}
	f.elevErr = devurl.ErrNoAgent
	a, tr := devurlsPage(t, f)
	a.devurlSetup(f.plan)
	waitDevurl(t, a)
	tr.Frame()
	d := a.settingsUI.data.devurls
	want := []string{"sudo", "/opt/omaterm", "proxy", "setup", "--system", "--ca", f.env.CAPath()}
	if !d.failed || !reflect.DeepEqual(d.terminal, want) || f.reloads != 0 {
		t.Fatalf("%+v, reloads %d", d, f.reloads)
	}
	if !tr.HasText("Run in a Terminal") || !tr.HasText("No password dialog could open") {
		t.Errorf("the page doesn't say so: %v", tr.Texts())
	}
}

func TestDevurlsCancelledAndFailedSteps(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{{devurl.ErrCancelled, "Cancelled"}, {errors.New("boom"), "The system step failed: boom"}} {
		f := useFakeDevurl(t, true)
		f.plan = []devurl.Change{{ID: "trust", Admin: true, Text: "Trust it system-wide"}}
		f.elevErr = tt.err
		a, _ := devurlsPage(t, f)
		a.devurlSetup(f.plan)
		waitDevurl(t, a)
		if d := a.settingsUI.data.devurls; !d.failed || !strings.HasPrefix(d.msg, tt.want) || f.reloads != 0 || len(d.terminal) != 0 {
			t.Errorf("%v: %+v", tt.err, d)
		}
	}
}

func TestDevurlsRemoveShowsOnlyWhenSetUp(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: []devurl.Check{{Name: "Certificate authority made", Detail: "not yet"}}}
	f.plan = somePlan
	_, tr := devurlsPage(t, f)
	if tr.HasText("Remove…") {
		t.Error("Remove shows with nothing set up")
	}
	f.status = daemon.ProxyStatus{Checks: someChecks}
	a, tr := devurlsPage(t, f)
	if err := tr.Click("Remove…"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if sh := a.settingsUI.sheet; sh == nil || !sh.danger || sh.confirm != "Remove" || len(sh.items) != 3 || sh.items[1][1] != "Asks for your password" {
		t.Fatalf("sheet %+v", sh)
	}
}

func TestDevurlsRemoveUndoesAndDeletesTheAuthority(t *testing.T) {
	f := useFakeDevurl(t, true)
	if _, err := devurl.LoadOrCreate(f.env.CADir); err != nil {
		t.Fatal(err)
	}
	a, _ := devurlsPage(t, f)
	a.devurlRemove()
	waitDevurl(t, a)
	d := a.settingsUI.data.devurls
	if d.failed || !strings.HasPrefix(d.msg, "Dev URLs is removed") {
		t.Fatalf("%+v", d)
	}
	if _, err := os.Stat(f.env.CAPath()); err == nil {
		t.Error("the authority is still there")
	}
	if len(f.elevated) != 0 {
		t.Errorf("the password was asked with nothing system-wide to remove: %v", f.elevated)
	}
}

func TestDevurlsTryAgainWhenOnlyTheProxyIsDown(t *testing.T) {
	f := useFakeDevurl(t, true)
	f.status = daemon.ProxyStatus{Checks: someChecks}
	a, tr := devurlsPage(t, f)
	if err := tr.Click("Try Again"); err != nil {
		t.Fatal(err)
	}
	waitDevurl(t, a)
	if f.reloads != 1 || a.settingsUI.data.devurls.failed {
		t.Fatalf("reloads %d, %+v", f.reloads, a.settingsUI.data.devurls)
	}
}

func TestDevurlsNeedsTheSessionDaemon(t *testing.T) {
	useFakeDevurl(t, false)
	a, tr := newSettingsTester(t, "devurls")
	defer stopTermPanes(a)
	if !tr.HasText("Needs the session daemon") || tr.HasText("Set Up…") || tr.HasText("Routes") {
		t.Fatalf("page without a daemon: %v", tr.Texts())
	}
}

func TestDevurlsPageIsInTheSidebarAfterWebAccess(t *testing.T) {
	var order []string
	for _, p := range settingsPages {
		if p.Parent == "" && p.Group == 1 {
			order = append(order, p.ID)
		}
	}
	if i := indexOf(order, "web"); i < 0 || order[i+1] != "devurls" {
		t.Fatalf("group 1: %v", order)
	}
	if _, ok := settingDefByID("devurlSetup"); !ok {
		t.Fatal("no search entry")
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// TestDevurlsShots draws the page, dark and light, with some checks and two
// routes, and the Set Up sheet, for a look beside the Web Access page:
// DEVURLS_SHOTS=dir go test -run TestDevurlsShots .
func TestDevurlsShots(t *testing.T) {
	dir := os.Getenv("DEVURLS_SHOTS")
	if dir == "" {
		t.Skip("set DEVURLS_SHOTS to a directory")
	}
	os.MkdirAll(dir, 0o755)
	defer setPalette(theme.BuiltinDark())
	shot := func(tr *ui.Tester, name string) {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tr.Image())
		f.Close()
	}
	for _, mode := range []string{"dark", "light"} {
		f := useFakeDevurl(t, true)
		f.status = daemon.ProxyStatus{Running: true, Addr: "127.0.0.1:8443", Checks: someChecks, Routes: someRoutes}
		f.plan = somePlan
		a, tr := devurlsPage(t, f)
		a.setThemeMode(mode)
		tr.SetSize(settingsWidth, settingsHeight)
		tr.Frame()
		tr.Frame()
		shot(tr, "devurls-"+mode)
		a.devurlSetupSheet()
		tr.Frame()
		tr.Frame()
		shot(tr, "devurls-sheet-"+mode)
		a.settingsUI.sheet = nil
		a.settingsUI.data.devurls.msg, a.settingsUI.data.devurls.failed = "No password dialog could open: run the step in a terminal, then press Try Again.", true
		a.settingsUI.data.devurls.terminal = []string{"sudo", "x"}
		tr.Frame()
		tr.Frame()
		shot(tr, "devurls-noagent-"+mode)
		a2, tr2 := newSettingsTester(t, "web")
		a2.setThemeMode(mode)
		tr2.Frame()
		tr2.Frame()
		shot(tr2, "web-"+mode)
		stopTermPanes(a2)
	}
}

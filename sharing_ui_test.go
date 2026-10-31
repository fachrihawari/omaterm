package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
	"omaterm/internal/tunnel"
)

// fakeShareHost is a process host that shares what it is told to, and
// lists what is shared: the session daemon's supervisor, without one.
type fakeShareHost struct {
	procs     []procs.Proc
	shared    []string // "project/name/via", as they were asked for
	unshared  []string // "project/name"
	shareErr  error
	unshareEr error
}

func (f *fakeShareHost) List(string) ([]procs.Proc, error) { return f.procs, nil }
func (f *fakeShareHost) Start(string, string) error        { return nil }
func (f *fakeShareHost) Stop(string, string) error         { return nil }
func (f *fakeShareHost) Restart(string, string) error      { return nil }
func (f *fakeShareHost) Trust(string, string) error        { return nil }
func (f *fakeShareHost) Sync()                             {}
func (f *fakeShareHost) Close()                            {}

func (f *fakeShareHost) Share(projectID, name, via string) error {
	if f.shareErr != nil {
		return f.shareErr
	}
	f.shared = append(f.shared, projectID+"/"+name+"/"+via)
	for i := range f.procs {
		if f.procs[i].Name == name {
			f.procs[i].Share, f.procs[i].ShareVia = "https://share-"+name+".trycloudflare.com", via
		}
	}
	return nil
}

func (f *fakeShareHost) Unshare(projectID, name string) error {
	if f.unshareEr != nil {
		return f.unshareEr
	}
	f.unshared = append(f.unshared, projectID+"/"+name)
	for i := range f.procs {
		if f.procs[i].Name == name {
			f.procs[i].Share, f.procs[i].ShareVia = "", ""
		}
	}
	return nil
}

// aShareApp is an app with one running process, web, served at its Dev
// URL, and a process host that shares it.
func aShareApp(t *testing.T, avail map[tunnel.Provider]string) (*appState, *fakeShareHost, *ui.Tester, string) {
	t.Helper()
	if avail != nil {
		prev := shareAvailable
		shareAvailable = func() map[tunnel.Provider]string { return avail }
		t.Cleanup(func() { shareAvailable = prev })
	}
	a := newTestApp(t)
	t.Cleanup(func() { stopTermPanes(a) })
	a.settings.OpenLinks = "pane"
	projectID := a.projects[a.active].ID
	web := procs.Proc{Def: procs.Def{Name: "web", Command: "npm run dev", Port: 5173, URL: "shop"},
		Status: procs.Running, Link: "https://shop.localhost", Trusted: true}
	host := &fakeShareHost{procs: []procs.Proc{web}}
	a.procHost = host
	a.procList[projectID] = host.procs
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	return a, host, tr, projectID
}

// openProcessMenu right-clicks a process's row and frames the menu.
func openProcessMenu(t *testing.T, tr *ui.Tester, row string) {
	t.Helper()
	rect, ok := tr.Find(row)
	if !ok {
		t.Fatalf("process row %q is missing: %v", row, tr.Texts())
	}
	tr.RightClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tr.Frame()
}

// A tunnel that can't work here is offered, disabled, saying why; one
// that can shares without a question.
func TestShareMenuAvailability(t *testing.T) {
	var copied []string
	prevCopy := copyText
	copyText = func(text string) { copied = append(copied, text) }
	t.Cleanup(func() { copyText = prevCopy })

	_, host, tr, projectID := aShareApp(t, map[tunnel.Provider]string{
		tunnel.Tailscale:  "",
		tunnel.Cloudflare: "cloudflared is not installed",
	})
	openProcessMenu(t, tr, "web")
	if !slices.Contains(tr.Menu(), "Share") {
		t.Fatalf("the row's menu has no Share: %v", tr.Menu())
	}
	cloudflare := "Anyone with the Link (Cloudflare): cloudflared is not installed"
	if err := tr.ChooseMenuItem("Share", cloudflare); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("a tunnel that can't work is not disabled: %v", err)
	}
	if len(host.shared) != 0 {
		t.Fatalf("a disabled tunnel was asked to share: %v", host.shared)
	}
	// Sharing with Tailscale asks nothing: only your own devices can open
	// it.
	if err := tr.ChooseMenuItem("Share", "Your Devices (Tailscale)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(host.shared) != 1 || host.shared[0] != projectID+"/web/tailscale" {
		t.Fatalf("the share asked for %v", host.shared)
	}
	if !tr.HasText("Shared web at ") {
		t.Fatalf("sharing said nothing: %v", tr.Texts())
	}
	if !tr.HasText("Share https://share-web.trycloudflare.com") {
		t.Fatalf("the row does not show the share: %v", tr.Texts())
	}
	_ = copied
}

// A Cloudflare link is public: the first share of a process asks, and the
// answer stands for the rest of the run.
func TestShareCloudflareAsksOnce(t *testing.T) {
	_, host, tr, projectID := aShareApp(t, map[tunnel.Provider]string{
		tunnel.Tailscale: "", tunnel.Cloudflare: "",
	})
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Share", "Anyone with the Link (Cloudflare)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !tr.HasText("Anyone with the link can open web.") || !tr.HasText("It stays shared while web runs.") {
		t.Fatalf("the Cloudflare question is missing: %v", tr.Texts())
	}
	if len(host.shared) != 0 {
		t.Fatalf("shared before the user agreed: %v", host.shared)
	}
	if err := tr.Click("Share"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(host.shared) != 1 || host.shared[0] != projectID+"/web/cloudflare" {
		t.Fatalf("the share asked for %v", host.shared)
	}
	// The second time this run asks nothing.
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Share", "Anyone with the Link (Cloudflare)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if tr.HasText("Anyone with the link can open web.") {
		t.Fatalf("asked again in the same run: %v", tr.Texts())
	}
	if len(host.shared) != 2 {
		t.Fatalf("the second share asked for %v", host.shared)
	}
}

// A shared process's row, tab and header show the share, which opens it.
func TestSharePillShows(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	projectID, tr := runProcesses(t, a)
	a.settings.OpenLinks = "pane"
	// What the daemon's tunnel gives a shared process, beside its link.
	a.procList[projectID][0].Link = "https://shop.localhost"
	a.procList[projectID][0].Share = "https://box.tailnet.ts.net:8443"
	a.procList[projectID][0].ShareVia = string(tunnel.Tailscale)
	tr.Frame()
	if _, ok := tr.Find("Share https://box.tailnet.ts.net:8443"); !ok {
		t.Fatalf("the row does not show the share: %v", tr.Texts())
	}
	a.showProcess(a.active, "web")
	tr.Frame()
	if n := countText(tr, "Share https://box.tailnet.ts.net:8443"); n < 3 {
		t.Fatalf("the share shows %d times, want the row, the tab and the header: %v", n, tr.Texts())
	}
	// The row settles as the pointer comes over it (its controls appear),
	// so the click follows a move.
	share, ok := tr.Find("Share https://box.tailnet.ts.net:8443")
	if !ok {
		t.Fatalf("the share pill is missing: %v", tr.Texts())
	}
	tr.Move(share.X+share.W/2, share.Y+share.H/2)
	tr.Frame()
	if err := tr.Click("Share https://box.tailnet.ts.net:8443"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	var opened bool
	for _, url := range a.paneURLs {
		if url == "https://box.tailnet.ts.net:8443" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("clicking the share opened nothing: %v", a.paneURLs)
	}
}

// A shared process's row menu stops the share, and says so.
func TestShareStop(t *testing.T) {
	a, host, tr, projectID := aShareApp(t, map[tunnel.Provider]string{tunnel.Tailscale: ""})
	host.procs[0].Share, host.procs[0].ShareVia = "https://box.tailnet.ts.net:8443", string(tunnel.Tailscale)
	a.procList[projectID] = host.procs
	tr.Frame()
	if _, ok := tr.Find("Share https://box.tailnet.ts.net:8443"); !ok {
		t.Fatalf("the row does not show the share: %v", tr.Texts())
	}
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Share", "Stop Sharing"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(host.unshared) != 1 || host.unshared[0] != projectID+"/web" {
		t.Fatalf("stopping the share asked for %v", host.unshared)
	}
	if !tr.HasText("Stopped sharing web") {
		t.Fatalf("stopping the share said nothing: %v", tr.Texts())
	}
	if _, ok := tr.Find("Share https://box.tailnet.ts.net:8443"); ok {
		t.Fatalf("the share stays on the row: %v", tr.Texts())
	}
}

// Copy Share Link copies the address, not the Dev URL.
func TestShareCopyLink(t *testing.T) {
	var copied []string
	prev := copyText
	copyText = func(text string) { copied = append(copied, text) }
	t.Cleanup(func() { copyText = prev })

	a, host, tr, projectID := aShareApp(t, map[tunnel.Provider]string{})
	host.procs[0].Share, host.procs[0].ShareVia = "https://lucky-words.trycloudflare.com", string(tunnel.Cloudflare)
	a.procList[projectID] = host.procs
	tr.Frame()
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Copy Share Link"); err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0] != "https://lucky-words.trycloudflare.com" {
		t.Fatalf("Copy Share Link copied %v", copied)
	}
}

// Without the session daemon, sharing is refused, and the refusal shows as
// a toast.
func TestShareNeedsTheDaemon(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prev := shareAvailable
	shareAvailable = func() map[tunnel.Provider]string { return map[tunnel.Provider]string{} }
	t.Cleanup(func() { shareAvailable = prev })

	a := newTestApp(t)
	defer stopTermPanes(a)
	_, tr := runProcesses(t, a)
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Share", "Your Devices (Tailscale)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !tr.HasText("Sharing web failed: sharing needs the session daemon, which is off") {
		t.Fatalf("the refusal is not shown: %v", tr.Texts())
	}
}

// A share that fails says why, as a toast.
func TestShareFailureShowsAsToast(t *testing.T) {
	_, host, tr, _ := aShareApp(t, map[tunnel.Provider]string{tunnel.Tailscale: ""})
	host.shareErr = errors.New("tailscale serve is not allowed on this tailnet")
	openProcessMenu(t, tr, "web")
	if err := tr.ChooseMenuItem("Share", "Your Devices (Tailscale)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !tr.HasText("Sharing web failed: tailscale serve is not allowed on this tailnet") {
		t.Fatalf("the failure is not shown: %v", tr.Texts())
	}
}

// The availability is read once and kept for a minute: menus open many
// times a second while the app runs.
func TestShareAvailabilityCached(t *testing.T) {
	prevAvail, prevNow := shareAvailable, nowFunc
	t.Cleanup(func() { shareAvailable, nowFunc = prevAvail, prevNow })
	reads := 0
	shareAvailable = func() map[tunnel.Provider]string {
		reads++
		return map[tunnel.Provider]string{tunnel.Tailscale: "Tailscale is signed out"}
	}
	at := nowFunc()
	nowFunc = func() time.Time { return at }

	a := newTestApp(t)
	defer stopTermPanes(a)
	for range 3 {
		if got := a.shareAvailability(); got[tunnel.Tailscale] != "Tailscale is signed out" {
			t.Fatalf("the answer was %v", got)
		}
	}
	if reads != 1 {
		t.Fatalf("asked %d times for an answer kept a minute", reads)
	}
	at = at.Add(2 * time.Minute)
	a.shareAvailability()
	if reads != 2 {
		t.Fatalf("a minute old answer was not read again: %d reads", reads)
	}
}

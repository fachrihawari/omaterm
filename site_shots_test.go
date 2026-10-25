package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/session"
	"omaterm/internal/web"
	"omaterm/service"
)

// The pictures for the README and the website: the shot suite's scenes,
// with something in the terminals and at twice the pixel density. They
// are not goldens; run with SITE_SHOTS=1 to refresh site/assets.

// Terminal screens the site shots feed into panes. They use the 16 ANSI
// colors, so each one follows the theme it is shown in.
const (
	siteAgentWaiting = "\x1b[1;36m❯\x1b[0m add a light variant to the theme loader\n\n" +
		"\x1b[32m●\x1b[0m Read \x1b[1minternal/theme/omarchy.go\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Read \x1b[1mthemewatch.go\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Parse the light flag from colors.toml\n" +
		"  and flip the wash when it is set.\n\n" +
		"\x1b[33m╭─ Edit internal/theme/omarchy.go ───────\x1b[0m\n" +
		"\x1b[33m│\x1b[0m \x1b[2m41\x1b[0m  func Parse(path string) Source {\n" +
		"\x1b[33m│\x1b[0m \x1b[31m42 -    bg := c.Background\x1b[0m\n" +
		"\x1b[33m│\x1b[0m \x1b[32m42 +    bg, light := c.Background,\x1b[0m\n" +
		"\x1b[33m│\x1b[0m \x1b[32m43 +        c.Light\x1b[0m\n" +
		"\x1b[33m╰────────────────────────────────────────\x1b[0m\n\n" +
		" \x1b[1mAllow this edit?\x1b[0m\n" +
		" \x1b[1;36m❯ 1. Yes\x1b[0m\n" +
		"   2. Yes, for this session\n" +
		"   3. No, and tell it what to do instead\n"

	siteDevServer = "\x1b[2m$\x1b[0m bun run dev\n\n" +
		"  \x1b[1;32mVITE\x1b[0m \x1b[32mv6.2.0\x1b[0m  ready in \x1b[1m312\x1b[0m ms\n\n" +
		"  \x1b[32m➜\x1b[0m  \x1b[1mLocal\x1b[0m:   \x1b[36mhttp://localhost:\x1b[1m5173\x1b[0;36m/\x1b[0m\n" +
		"  \x1b[2m➜  Network: use --host to expose\x1b[0m\n\n" +
		"\x1b[2m12:04:31\x1b[0m \x1b[1;36m[vite]\x1b[0m \x1b[32mhmr update\x1b[0m \x1b[2m/src/App.tsx\x1b[0m\n" +
		"\x1b[2m12:04:58\x1b[0m \x1b[1;36m[vite]\x1b[0m \x1b[32mhmr update\x1b[0m \x1b[2m/src/theme.css\x1b[0m\n" +
		"\x1b[2m12:05:12\x1b[0m \x1b[1;36m[vite]\x1b[0m \x1b[32mhmr update\x1b[0m \x1b[2m/src/Pane.tsx\x1b[0m\n"

	siteAgentDone = "\x1b[1;36m❯\x1b[0m make the theme follow Omarchy theme switches\n\n" +
		"\x1b[32m●\x1b[0m Update \x1b[1minternal/theme/omarchy.go\x1b[0m  \x1b[32m+42\x1b[0m \x1b[31m-7\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Update \x1b[1mthemewatch.go\x1b[0m  \x1b[32m+31\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Write  \x1b[1minternal/theme/omarchy_test.go\x1b[0m  \x1b[32m+7\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Bash   go test ./internal/theme/...\n" +
		"  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m  omaterm/internal/theme  0.214s\n\n" +
		"Done. omaterm now watches the current Omarchy theme and\n" +
		"reloads its palette whenever \x1b[1momarchy-theme-set\x1b[0m runs,\n" +
		"no restart needed.\n\n" +
		"\x1b[1;36m❯\x1b[0m "

	siteShell = "\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[35mmain\x1b[0m \x1b[32m❯\x1b[0m git status --short\n" +
		" \x1b[31mM\x1b[0m statusbar.go\n" +
		"\x1b[32mM\x1b[0m  main.go\n" +
		"\x1b[32mM\x1b[0m  theme.go\n" +
		"\x1b[31m??\x1b[0m icons/alert-triangle.svg\n\n" +
		"\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[35mmain\x1b[0m \x1b[32m❯\x1b[0m go test ./...\n" +
		"\x1b[32mok\x1b[0m  \tomaterm\t\t\t\t7.192s\n" +
		"\x1b[32mok\x1b[0m  \tomaterm/internal/gitsnap\t0.081s\n" +
		"\x1b[32mok\x1b[0m  \tomaterm/internal/session\t1.304s\n" +
		"\x1b[32mok\x1b[0m  \tomaterm/internal/theme\t\t0.214s\n" +
		"\x1b[32mok\x1b[0m  \tomaterm/service\t\t\t0.552s\n\n" +
		"\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[35mmain\x1b[0m \x1b[32m❯\x1b[0m "

	siteTests = "\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[32m❯\x1b[0m go test -run Theme ./...\n" +
		"\x1b[32m=== RUN\x1b[0m   TestThemeParse\n" +
		"\x1b[32m--- PASS\x1b[0m: TestThemeParse (0.00s)\n" +
		"\x1b[32m=== RUN\x1b[0m   TestThemeWatch\n" +
		"\x1b[32m--- PASS\x1b[0m: TestThemeWatch (0.12s)\n" +
		"\x1b[32mPASS\x1b[0m\n" +
		"\x1b[32mok\x1b[0m  \tomaterm/internal/theme\t0.214s\n"
)

// siteFeed writes screens into the active tab's panes, in pane order.
func siteFeed(t *testing.T, a *appState, screens ...string) {
	t.Helper()
	ids := a.tabs[a.activeTab].Split.termIDs()
	for i, s := range screens {
		if i >= len(ids) {
			break
		}
		term := a.term(ids[i])
		if term == nil {
			t.Fatalf("pane %s did not start", ids[i])
		}
		term.Feed([]byte(strings.ReplaceAll(s, "\n", "\r\n")))
	}
}

func TestSiteShots(t *testing.T) {
	if os.Getenv("SITE_SHOTS") == "" {
		t.Skip("set SITE_SHOTS=1 to render the site's pictures")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	split3 := func(a *appState) {
		a.tabs[a.activeTab].Split = &splitNode{
			Dir: splitVertical, Ratio: 55,
			First: &splitNode{TermID: randomTermID()},
			Second: &splitNode{Dir: splitHorizontal, Ratio: 50,
				First:  &splitNode{TermID: randomTermID()},
				Second: &splitNode{TermID: randomTermID()},
			},
		}
	}
	shots := []struct {
		name    string
		setup   func(a *appState)
		screens []string
	}{
		{"hero", shotStatuses, []string{siteAgentWaiting, siteDevServer}},
		{"changes", func(a *appState) {
			shotChanges(a)
			// The shot's pinned path does not exist; hold the list against
			// this repo, or the frame after the feed reloads it as empty.
			a.changes.root = wd
			a.changes.stamp = service.GitStamp(wd)
		}, []string{siteAgentDone}},
		{"split", func(a *appState) {
			shotStatuses(a) // the other sessions' statuses
			split3(a)
			a.inspectorVisible = false
			ids := a.tabs[a.activeTab].Split.termIDs()
			since := nowFunc().Add(-2 * time.Minute).UnixMilli()
			for i, info := range []session.Info{
				{Status: session.Waiting, Agent: "claude", Process: "claude", Kind: "permission", Unread: true},
				{Status: session.Working, Process: "bun", Title: "vite", Progress: -1},
				{Status: session.Idle, Process: "go", Message: "Tests passed", Unread: true, Progress: -1},
			} {
				info.ID, info.Session, info.Since = ids[i], a.tabs[a.activeTab].ID, since
				info.Dir = "/home/you/Projects/omaterm"
				a.status[ids[i]] = info
			}
			a.activePaneTermID = ids[0]
		}, []string{siteAgentWaiting, siteDevServer, siteTests}},
		{"git", func(a *appState) { a.inspectorTab = 2 }, []string{siteShell}},
		{"theme-tokyo", func(a *appState) { a.applyTheme(shotTheme(t, "tokyo-night")); shotStatuses(a) },
			[]string{siteAgentWaiting, siteDevServer}},
		{"theme-light", func(a *appState) { a.applyTheme(shotTheme(t, "lupine")); shotStatuses(a) },
			[]string{siteAgentWaiting, siteDevServer}},
		{"editor", func(a *appState) { a.openEditor("app.go") }, nil},
		{"diff", func(a *appState) {
			a.setDiffBuffers(
				"package main\n\nfunc greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"world\"))\n}\n",
				"package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"omaterm\"))\n}\n",
				"main.go")
			a.diffTitle = "main.go · Working Tree · INDEX ↔ WORKING TREE"
			a.diffSrc = diffSource{Kind: diffWorking, Path: "main.go"}
			a.focusDiffTab("main.go")
		}, nil},
		{"graph", func(a *appState) {
			a.activeView = tabGraph
			a.commits = shotMergeCommits()
			a.graphLoaded = true
		}, nil},
		{"palette", func(a *appState) { a.openOverlay(overlayCommandPalette) }, []string{siteShell}},
		{"info", func(a *appState) {
			shotStatuses(a)
			a.inspectorVisible, a.inspectorTab = true, 0
			// What the sessions run, not the goldens' stand-in.
			a.processes = []service.Process{
				{Name: "claude", Pid: 48211, CPU: "1.2%", Mem: "212 MB"},
				{Name: "bun", Pid: 48305, CPU: "0.8%", Mem: "96 MB"},
				{Name: "codex", Pid: 48390, CPU: "3.1%", Mem: "148 MB"},
			}
			a.ports = []service.Port{{Port: 5173, Name: "http", Open: true, Pid: 48305, Process: "bun"}}
		}, []string{siteAgentWaiting, siteDevServer}},
		{"pair", func(a *appState) {
			shotStatuses(a)
			link := "https://box.tailnet.ts.net/#code=K7QM-4XWD"
			rows, err := web.QR(link)
			if err != nil {
				t.Fatal(err)
			}
			open := true
			a.webOn, a.webPairOpen = true, &open
			a.webPair = &webPairing{ready: true, link: link, code: "K7QM-4XWD", qr: rows,
				expires: nowFunc().Add(10 * time.Minute)}
		}, []string{siteAgentWaiting, siteDevServer}},
	}
	dir := filepath.Join("site", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range shots {
		a := newShotApp(wd)
		s.setup(a)
		tr := ui.NewTester(a.view, 1440, 900)
		tr.SetScale(2)
		siteFeed(t, a, s.screens...)
		tr.Frame()
		f, err := os.Create(filepath.Join(dir, s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tr.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
		stopTermPanes(a)
	}

	// The Settings window, on its Terminal page.
	a := newShotApp(wd)
	a.openSettings("terminal")
	tr := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	tr.SetScale(2)
	tr.Frame()
	tr.Frame()
	f, err := os.Create(filepath.Join(dir, "settings.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tr.Image()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	stopTermPanes(a)
}

package main

// Browser panes: a pane of a session that shows a web page (the system's
// web view, WebKitGTK on Linux) instead of a shell, beside the agent that
// builds the page. `omaterm browser` drives it (automate.go). Adapted from
// Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// browser.go.

import (
	"errors"
	"os/exec"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// webEntry is a browser pane's web view.
type webEntry struct {
	view *mygo.WebView
	err  string
	// url and title are the page's, as it last committed.
	url, title string
	loading    bool
	back, fwd  bool
	// address is the address bar's text: the page's URL, unless the user
	// is typing in it (editing); focusAddress has it take the keyboard.
	address      string
	editing      bool
	focusAddress bool
	focused      bool
	// yieldKeys hides the page for a frame, which gives the keyboard back
	// to the window: a shortcut the page took opens what types in the
	// window, as the find bar or the palette.
	yieldKeys bool
	// The find bar: shown, its query, and its matches.
	finding  bool
	query    string
	found    mygo.FindResult
	findSeq  int
	findText string
	// loads are omaterm browser's calls waiting for the page's load to end.
	loads []chan error
}

// isBrowser tells a browser pane.
func (a *appState) isBrowser(id string) bool {
	_, ok := a.paneURLs[id]
	return ok
}

// ensureWeb makes a browser pane's web view, on its page.
func (a *appState) ensureWeb(id string) *webEntry {
	if e := a.webs[id]; e != nil {
		return e
	}
	url := a.paneURLs[id]
	e := &webEntry{url: url, address: url, title: a.paneTitles[id]}
	if url == proto.BlankURL {
		e.address, e.focusAddress = "", true
	}
	a.webs[id] = e
	if a.win == nil {
		return e
	}
	secret := newID() + newID()
	v, err := a.win.NewWebView(mygo.WebViewOptions{
		Page: mygo.PageOptions{DevTools: mygo.DevToolsEnabled, PreloadScript: pageKeysScript(secret)},
	})
	if err != nil {
		e.err = err.Error()
		return e
	}
	e.view = v
	page := v.Page()
	// The app's shortcuts, which the page took (webkeys.go).
	page.OnWillNavigate(func(n *mygo.NavigateEvent) {
		if cmd, ok := pageKey(n, secret); ok {
			a.post(func() {
				if a.webs[id] != e {
					return
				}
				a.activePaneTermID = id
				a.queuedActions = append(a.queuedActions, cmd)
				switch cmd {
				case "browser.reload", "browser.back", "browser.forward":
				default:
					e.yieldKeys = true
				}
			})
		}
	})
	// The page's events come on the UI thread: they change what the pane
	// shows, and what is saved of it.
	page.OnDidNavigate(func(url string) {
		a.post(func() {
			if a.webs[id] != e {
				return
			}
			e.url, e.err = url, ""
			if !e.editing {
				e.address = url
			}
			e.syncNav()
			a.tellPage(id, e)
		})
	})
	page.OnPageTitleUpdated(func(t *mygo.TitleEvent) {
		a.post(func() {
			if a.webs[id] != e {
				return
			}
			e.title = t.Title
			// A page of an app changes its URL without navigating.
			if url := page.URL(); url != "" {
				e.url = url
				if !e.editing {
					e.address = url
				}
			}
			e.syncNav()
			a.tellPage(id, e)
		})
	})
	page.OnDidFinishLoad(func() {
		a.post(func() {
			e.syncNav()
			e.loadEnded(nil)
		})
	})
	page.OnDidFailLoad(func(err *mygo.LoadError) {
		a.post(func() {
			e.syncNav()
			// A navigation the user left before it ended fails too.
			if err.Code != -999 && err.Description != "" {
				e.err = err.Description
				e.loadEnded(errors.New(err.Description))
			}
		})
	})
	page.OnRenderProcessGone(func(string) { page.Reload() })
	if url != proto.BlankURL {
		e.load(url)
	}
	return e
}

// syncNav reads where the page can go and whether it loads.
func (e *webEntry) syncNav() {
	if e.view == nil || e.view.IsDestroyed() {
		return
	}
	page := e.view.Page()
	e.back, e.fwd, e.loading = page.CanGoBack(), page.CanGoForward(), page.IsLoading()
}

// load goes to what the user typed, as a URL or a search.
func (e *webEntry) load(text string) {
	url := proto.BrowserURL(text)
	e.url, e.address, e.err = url, url, ""
	if e.view == nil {
		return
	}
	if err := e.view.Page().LoadURL(url); err != nil {
		e.err = err.Error()
	}
	e.loading = true
}

// tellPage keeps the page a browser pane shows, for a restore, and the
// title that names it.
func (a *appState) tellPage(id string, e *webEntry) {
	if e.url == "" || a.paneURLs[id] == e.url && a.paneTitles[id] == e.title {
		return
	}
	a.paneURLs[id], a.paneTitles[id] = e.url, e.title
	a.saveSoon()
	if a.host != nil {
		a.host.changed()
	}
}

// dropWeb closes a browser pane's page.
func (a *appState) dropWeb(id string) {
	if e := a.webs[id]; e != nil {
		if e.view != nil {
			e.view.Destroy()
		}
		delete(a.webs, id)
	}
	delete(a.paneURLs, id)
	delete(a.paneTitles, id)
}

// browserInfo is what a browser pane tells of itself, as a shell's Info.
func (a *appState) browserInfo(id, sessionID string) session.Info {
	i := session.Info{ID: id, Session: sessionID, Status: session.Idle, Progress: -1, URL: a.paneURLs[id], Title: a.paneTitles[id]}
	if e := a.webs[id]; e != nil {
		i.Title = e.title
		if e.loading {
			i.Status = session.Working
		}
	}
	return i
}

// openBrowser opens a browser pane beside the focused pane, or gives the
// address bar of the browser the user is in the keyboard.
func (a *appState) openBrowser(url string) {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || a.tabs[a.activeTab].Kind != tabTerminal {
		return
	}
	at := a.activeSplitLeaf()
	if e := a.webs[at]; e != nil && url == "" {
		e.focusAddress = true
		return
	}
	if url == "" {
		url = proto.BlankURL
	}
	tab := &a.tabs[a.activeTab]
	id := newID()
	a.paneURLs[id] = proto.BrowserURL(url)
	if url == proto.BlankURL {
		a.paneURLs[id] = proto.BlankURL
	}
	tab.Split = splitAt(tab.Split, at, id, splitVertical, false, 0.5)
	tab.Zoomed = ""
	a.activePaneTermID = id
	a.saveSoon()
}

// browserCard is a browser pane's content: its toolbar, which is its
// header, over its page; it reports whether the pane is being dragged.
func (a *appState) browserCard(c *ui.Context, tab *topTab, id string, multi, focused, hovered bool) (dragging bool) {
	e := a.ensureWeb(id)
	blank := e.url == proto.BlankURL
	dragging = a.browserBar(c, tab, id, e, multi, focused, hovered)
	ui.Box(c.Key("body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		switch {
		case blank:
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Icon(c, iconGlobe).Size(34, 34).TextColor(pal.TextFaint)
				ui.Text(c, "Type an address, or words to search for").FontSize(13).TextColor(pal.TextMuted)
			})
		case e.view == nil:
			ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
				ui.Icon(c, iconGlobe).Size(30, 30).TextColor(pal.TextFaint)
				ui.Text(c, "This pane cannot show web pages").FontSize(13).FontWeight(600).TextColor(pal.Text)
				msg := e.err
				if msg == "" {
					msg = "Install webkit2gtk-4.1 to use browser panes."
				}
				ui.Text(c, msg).FontSize(12).TextColor(pal.TextMuted).TextAlign(ui.Center)
			})
		case e.yieldKeys:
			// Counted as focused, the page is not handed the keyboard again
			// while the pane keeps the window's focus: only as it comes
			// back to the pane.
			e.yieldKeys = false
			e.focused = true
			ui.Box(c.Key("web-yield")).Grow(1)
			a.post(func() {}) // the page shows again next frame
		default:
			ref := a.handle("web-" + id)
			web := ui.WebView(c.Key("web"), e.view).Grow(1).Bind(ref)
			if web.Hovered() && !focused && a.settings.FocusFollowsMouse {
				a.activePaneTermID = id
			}
			switch {
			case ref.Focused(c) && !e.focused:
				e.focused = true
				e.view.Focus()
				a.activePaneTermID = id
			case !ref.Focused(c):
				e.focused = false
			}
			if e.err != "" {
				a.browserError(c, e)
			}
			if e.finding {
				a.browserFind(c, e)
			}
		}
	})
	return dragging
}

// browserBar is a browser pane's header: back, forward, reload and the
// address bar, whose globe drags the pane, then the pane's buttons.
func (a *appState) browserBar(c *ui.Context, tab *topTab, id string, e *webEntry, multi, focused, hovered bool) (dragging bool) {
	h := ui.Row(c.Key("hdr")).Height(a.headerH()+4).Shrink(0).AlignItems(ui.Center).Padding(0, 6, 0, 6).Gap(2)
	if h.Pressed() && !focused {
		a.activePaneTermID = id
	}
	h.Children(func() {
		navButton(c, "back", iconChevronLeft, "Back", e.back, func() { e.view.Page().GoBack() })
		navButton(c, "forward", iconChevronRight, "Forward", e.fwd, func() { e.view.Page().GoForward() })
		if e.loading {
			navButton(c, "stop", iconX, "Stop", true, func() { e.view.Page().Stop(); e.loading = false })
			c.After(500 * time.Millisecond) // a load's end may come late
			e.syncNav()
		} else {
			navButton(c, "reload", iconRefresh, "Reload", e.view != nil && e.url != proto.BlankURL, func() {
				e.view.Page().Reload()
				e.loading = true
			})
		}
		dragging = a.addressBar(c, tab, id, e, multi)
		if e.url != proto.BlankURL {
			navButton(c, "external", iconExternalLink, "Open in Default Browser", true, func() {
				// Waited for, as it hands over and exits: each click left
				// a zombie process.
				if cmd := exec.Command("xdg-open", e.url); cmd.Start() == nil {
					go cmd.Wait()
				}
			})
		}
		a.paneControls(c, tab, id, (hovered || focused && multi) && !dragging, multi)
	})
	return dragging
}

// navButton is a toolbar's button, faint while it can do nothing.
func navButton(c *ui.Context, key string, svg *ui.SVG, label string, enabled bool, fn func()) {
	b := ui.ButtonBase(c.Key(key)).Size(26, 24).Radius(6).Center().TextColor(pal.TextMuted).Label(label).Tooltip(label).Disabled(!enabled).Shrink(0)
	switch {
	case !enabled:
		// Disabled, it paints at half opacity: dim enough.
	case b.Hovered():
		b.Background(pal.Hover).TextColor(pal.Text)
	}
	b.Children(func() { ui.Icon(c, svg).Size(14, 14) })
	if enabled && b.Clicked() {
		fn()
	}
}

// addressBar is where the page's URL shows, and the user types another, or
// words to search for. Its globe is the pane's handle with several panes.
func (a *appState) addressBar(c *ui.Context, tab *topTab, id string, e *webEntry, multi bool) (dragging bool) {
	blank := e.url == proto.BlankURL
	frame := ui.Row(c.Key("address")).Grow(1).MinWidth(120).Height(26).Padding(0, 10, 0, 3).Gap(3).Radius(8).
		AlignItems(ui.Center).Background(pal.Input).Border(1, pal.Border).Cursor(ui.CursorText)
	var in ui.Element
	frame.Children(func() {
		grip := ui.Row(c.Key("grip")).Size(20, 20).Radius(6).Center().Shrink(0).TextColor(pal.TextFaint)
		if multi {
			grip.Drag(paneDrag{tab.ID, id}).Tooltip("Drag to move the pane")
			dragging = grip.Dragging()
			switch {
			case dragging:
				floating(grip.Background(pal.Popover))
			case grip.Hovered():
				grip.Background(pal.Hover).Cursor(ui.CursorGrab)
			}
		}
		grip.Children(func() { ui.Icon(c, iconGlobe).Size(13, 13) })
		in = ui.TextInputBase(c.Key("in"), &e.address).Grow(1).MinWidth(0).FontSize(12.5).
			Placeholder("Address or search").Label("Address")
		if blank {
			in.Bind(a.handle("term-" + id))
		}
	})
	focused := in.Focused()
	e.editing = focused
	glow := a.animate(frame, "glow", b2f(focused), 120*time.Millisecond)
	if glow > 0 {
		frame.Shadow(0, 0, 0, 3*glow, pal.Accent.Alpha(0.3*glow))
	}
	if frame.Pressed() && !focused {
		in.Focus()
	}
	if e.focusAddress {
		e.focusAddress = false
		in.Focus().SetTextSelection(0, utf8.RuneCountInString(e.address))
		a.activePaneTermID = id
	}
	if focused {
		a.activePaneTermID = id
	}
	if in.Submitted() && e.address != "" {
		e.load(e.address)
		e.focused = false
		a.handle("web-" + id).Focus()
		a.tellPage(id, e)
	}
	if frame.Shortcut(0, ui.KeyEscape) {
		e.address = e.url
		if e.url == proto.BlankURL {
			e.address = ""
		}
	}
	if !focused && e.url != proto.BlankURL {
		e.address = e.url
	}
	return dragging
}

// browserError shows why the page failed to load, over its top.
func (a *appState) browserError(c *ui.Context, e *webEntry) {
	bar := ui.Row(c.Key("error")).Absolute().Top(8).Left(12).Right(12).Padding(8, 8, 8, 12).Gap(8).
		AlignItems(ui.Center).Radius(10).Background(pal.Popover)
	floating(bar).Children(func() {
		ui.Text(c, e.err).FontSize(12.5).TextColor(pal.Bad).Grow(1).MinWidth(0).Ellipsis("…").SingleLine()
		retry := ui.ButtonBase(c.Key("retry")).Height(26).Padding(0, 10).Radius(7)
		face(retry, pal.Raised).Children(func() {
			ui.Text(c, "Try Again").FontSize(12).TextColor(pal.Text)
		})
		if retry.Clicked() {
			e.load(e.url)
		}
		navButton(c, "dismiss", iconX, "Dismiss", true, func() { e.err = "" })
	})
}

// browserFind is the find bar over a page.
func (a *appState) browserFind(c *ui.Context, e *webEntry) {
	if e.findText != e.query {
		a.findInPage(e, false, false)
	}
	bar := ui.Row(c.Key("find")).Absolute().Top(8).Right(12).Height(34).Padding(0, 4, 0, 12).Gap(4).
		AlignItems(ui.Center).Radius(10).Background(pal.Popover).Label("Find")
	floating(bar).Children(func() {
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(pal.TextMuted).Shrink(0)
		in := ui.TextInputBase(c.Key("q"), &e.query).Width(180).FontSize(13).Placeholder("Find in page").AutoFocus()
		if in.Submitted() {
			a.findInPage(e, true, false)
		}
		count := ""
		switch {
		case e.query == "":
		case e.found.Matches == 0:
			count = "No matches"
		default:
			count = itoa(e.found.Active) + " of " + itoa(e.found.Matches)
		}
		ui.Text(c, count).FontSize(12).TextColor(pal.TextFaint).SingleLine().MinWidth(56).TextAlign(ui.End).Margin(0, 4, 0, 0)
		navButton(c, "previous", iconArrowUp, "Previous", true, func() { a.findInPage(e, true, true) })
		navButton(c, "next", iconArrowDown, "Next", true, func() { a.findInPage(e, true, false) })
		navButton(c, "close", iconX, "Close", true, func() { e.finding = false; e.query = ""; a.findInPage(e, false, false) })
	})
	if bar.Shortcut(0, ui.KeyEscape) {
		e.finding, e.query = false, ""
		a.findInPage(e, false, false)
	}
}

// findInPage searches the page off the UI thread, where the page answers.
func (a *appState) findInPage(e *webEntry, next, back bool) {
	if e.view == nil {
		return
	}
	q := e.query
	e.findText = q
	e.findSeq++
	seq, page := e.findSeq, e.view.Page()
	if q == "" {
		e.found = mygo.FindResult{}
		go page.StopFindInPage()
		return
	}
	go func() {
		res, err := page.FindInPage(q, mygo.FindOptions{FindNext: next, Backward: back})
		a.post(func() {
			if err == nil && e.findSeq == seq {
				e.found = res
			}
		})
	}()
}

// findInFocused opens the find bar of the browser pane the user is in.
func (a *appState) findInFocused() {
	if e := a.webs[a.activeSplitLeaf()]; e != nil {
		e.finding = true
	}
}

// focusedWeb is the browser pane the user is in, nil for none.
func (a *appState) focusedWeb() *webEntry {
	if a.activeView != tabTerminal {
		return nil
	}
	return a.webs[a.activeSplitLeaf()]
}

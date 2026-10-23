package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

func main() {
	// Updates come from the signed manifests of the GitHub releases named in
	// mygo.config.ts. The native window is the one for this app: oterm shows
	// no web page, so it needs no webview to offer an update. The app's own
	// Info panel and palette check from there; a menu bar would not exist on
	// Linux, where oterm mostly runs.
	mygo.Use(native.Plugin)
	updatesReady = true

	mygo.App.WhenReady(func() {
		a := newApp()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "oterm",
			Width:           1440,
			Height:          900,
			MinWidth:        900,
			MinHeight:       560,
			BackgroundColor: "#0b0e12",
			StateKey:        "main",
			Content:         ui.View(a.view),
		})
		// Now that the window exists, kick the initial async loads.
		a.win.OnClosed(func() {
			a.stopSystemStats()
			for _, t := range a.termPanes {
				if t != nil {
					t.Close()
				}
			}
		})
		// Git status goes stale while the app is in the background; reload
		// it when the window comes back instead of asking for a button.
		a.win.OnFocus(func() {
			if a.inspectorVisible && a.inspectorTab == 2 {
				a.refreshActiveRepo()
			}
		})
		if len(a.projects) > 0 {
			a.setProject(0)
		}
		a.startSystemStats()
		if a.inspectorTab == 0 && a.inspectorVisible {
			a.startProcessRefresh()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

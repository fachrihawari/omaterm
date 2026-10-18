package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		a := newApp()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "omaterm",
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
			for _, t := range a.termPanes {
				if t != nil {
					t.Close()
				}
			}
		})
		if len(a.projects) > 0 {
			a.setProject(0)
			if a.inspectorTab == 0 && a.inspectorVisible {
				a.startProcessRefresh()
			}
		} else {
			a.openProjectDialog()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

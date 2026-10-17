package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "omaterm",
			Width:           1440,
			Height:          900,
			MinWidth:        900,
			MinHeight:       560,
			BackgroundColor: "#0b0e12",
			StateKey:        "main",
			Content:         ui.View(newApp().view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

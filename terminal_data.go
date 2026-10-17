package main

// Terminal pane contents, transcribed from the mock's pane markup. Top is
// the mock's mt-* margin in DIPs.

// term color indexes
const (
	tcDefault = -1
	tcGreen   = iota
	tcLime
	tcPurple
	tcYellow
	tcCyan
	tcBlue
	tcOrange
	tcRed
	tcDim
	tcMuted
	tcWhite
	tcLink
)

func tx(text string, color int) span { return span{Text: text, Color: color} }
func tp(text string) span            { return span{Text: text, Color: tcDefault} }

func mockPanes() [3]paneData {
	p1 := paneData{
		Title: "bun --filter web dev", Shell: "fish", Pid: 27682, Cursor: true,
		Lines: []termLine{
			{Spans: []span{tx("omaterm", tcCyan), tx(" on ", tcMuted), tx(" main*", tcPurple), tx(" via ", tcMuted), tx("bun v1.3.13", tcOrange)}},
			{Spans: []span{tx("❯", tcGreen), tp(" bun --filter web dev")}},
			{Top: 8, Spans: []span{tp("web dev "), tx("$", tcMuted), tp(" vite dev")}},
			{Top: 16, Spans: []span{tx("VITE v6.1.5", tcWhite), tx(" ready in ", tcMuted), tx("1609 ms", tcGreen)}},
			{Top: 12, Spans: []span{tx("➜ ", tcMuted), tp("Local:   "), tx("http://localhost:3000/", tcBlue)}},
			{Spans: []span{tx("➜ ", tcMuted), tp("Network: "), tx("use --host to expose", tcMuted)}},
			{Spans: []span{tx("➜ ", tcMuted), tp("Debug:   "), tx("http://localhost:3000/__debug", tcBlue)}},
			{Top: 12, Spans: []span{tx("└─ Running...", tcCyan)}},
			{Top: 32, Spans: []span{tx("[vite] connected.", tcMuted)}},
			{Spans: []span{tx("[vite] hmr update /src/components/GitPanel.tsx", tcMuted)}},
		},
	}
	p2 := paneData{
		Title: "pnpm api:dev", Shell: "fish", Pid: 27901,
		Lines: []termLine{
			{Spans: []span{tx("❯", tcGreen), tp(" pnpm api:dev")}},
			{Top: 8, Spans: []span{tx("> api@1.0.0 dev", tcMuted)}},
			{Spans: []span{tx("> tsx watch src/server.ts", tcMuted)}},
			{Top: 12, Spans: []span{tx("✓", tcGreen), tp(" database connected")}},
			{Spans: []span{tx("✓", tcGreen), tp(" queues initialized")}},
			{Spans: []span{tx("✓", tcGreen), tp(" server listening "), tx(":4000", tcBlue)}},
		},
	}
	p3 := paneData{
		Title: "tail -f logs/app.log", Shell: "fish", Dim: true,
		Lines: []termLine{
			{Spans: []span{tx("05:41:22", tcDim), tp(" INFO"), tx(" request GET /api/projects 200 18ms", tcMuted)}},
			{Spans: []span{tx("05:41:26", tcDim), tp(" INFO"), tx(" ws client connected session=dev", tcMuted)}},
			{Spans: []span{tx("05:41:31", tcDim), tp(" INFO"), tx(" git watcher changed src/components/GitPanel.tsx", tcMuted)}},
			{Spans: []span{tx("05:41:31", tcDim), tp(" INFO"), tx(" hmr broadcast clients=1", tcMuted)}},
			{Spans: []span{tx("05:41:34", tcDim), tp(" DEBUG"), tx(" terminal resize cols=82 rows=17", tcMuted)}},
		},
	}
	return [3]paneData{p1, p2, p3}
}

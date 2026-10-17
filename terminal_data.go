package main

// Terminal pane contents, transcribed from the mock's pane markup.

// term color indexes
const (
	tcDefault = -1
	tcGreen   = iota
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
		Title: "bun --filter web dev", Shell: "fish", Pid: 27682,
		Lines: []termLine{
			{Spans: []span{tx("omaterm", tcCyan), tp(" on "), tx(" ", tcPurple), tx("main*", tcPurple), tp(" via "), tx("bun", tcYellow), tx(" v1.3.13", tcOrange)}},
			{Spans: []span{tx("❯", tcGreen), tp(" bun --filter web dev")}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("web dev", tcDim), tx(" $ vite dev", tcMuted)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("  VITE v6.1.5", tcGreen), tx("  ready in ", tcDefault), tx("1609", tcYellow), tx(" ms", tcDefault)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("  → Local:   ", tcDim), tx("http://localhost:3000/", tcLink)}},
			{Spans: []span{tx("  → Network: ", tcDim), tx("use --host to expose", tcMuted)}},
			{Spans: []span{tx("  → Debug:   ", tcDim), tx("http://localhost:3000/__debug", tcLink)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("  ⎿  Running…", tcMuted)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("[vite] connected.", tcMuted)}},
			{Spans: []span{tx("[vite] hmr update /src/components/GitPanel.tsx", tcMuted)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("❯", tcGreen)}},
		},
	}
	p2 := paneData{
		Title: "pnpm api:dev", Shell: "fish", Pid: 27901,
		Lines: []termLine{
			{Spans: []span{tx("❯", tcGreen), tp(" pnpm api:dev")}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("> api@1.0.0 dev", tcMuted)}},
			{Spans: []span{tx("> tsx watch src/server.ts", tcMuted)}},
			{Spans: []span{tp("")}},
			{Spans: []span{tx("✓", tcGreen), tp(" database connected")}},
			{Spans: []span{tx("✓", tcGreen), tp(" queues initialized")}},
			{Spans: []span{tx("✓", tcGreen), tp(" server listening "), tx(":4000", tcYellow)}},
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

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	xdraw "golang.org/x/image/draw"

	"omaterm/internal/model"
	"omaterm/internal/session"
	"omaterm/internal/web"
	"omaterm/internal/worktree"
	"omaterm/service"
)

// Short promo videos for posts about omaterm: the site shots' scenes,
// played out frame by frame. Run scripts/site-video.sh, which sets
// SITE_VIDEO (the clip), SITE_VIDEO_FORMAT (wide or tall) and
// SITE_VIDEO_DIR, then turns the frames into an MP4. The test writes
// numbered PNGs and captions.txt (start, end, text per line, in seconds;
// " | " marks where a tall video's caption breaks) to SITE_VIDEO_DIR.

const videoFPS = 30

// videoFormats are the window sizes the clips render at: wide is
// 1920×1080, tall the 1080×1100 under a 4:5 video's caption band, and
// site the website's 1920×1200, the size of its pictures' window.
var videoFormats = map[string]struct {
	w, h  int
	scale float32
}{
	"wide": {1280, 720, 1.5},
	"tall": {864, 880, 1.25},
	"site": {1280, 800, 1.5},
}

// A Codex session asking to run a command, for the status clip.
const siteCodexWaiting = "\x1b[1m›\x1b[0m move the settings loader into its own package\n\n" +
	"\x1b[2m•\x1b[0m Edited \x1b[1minternal/settings/load.go\x1b[0m \x1b[32m+64\x1b[0m\n" +
	"\x1b[2m•\x1b[0m Edited \x1b[1msettings.go\x1b[0m \x1b[32m+3\x1b[0m \x1b[31m-58\x1b[0m\n" +
	"\x1b[2m•\x1b[0m Ran \x1b[1mgo build ./...\x1b[0m\n" +
	"  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m\n\n" +
	"\x1b[1mAllow Codex to run this command?\x1b[0m\n\n" +
	"  \x1b[2m$\x1b[0m go mod tidy\n\n" +
	"\x1b[1;36m› 1. Yes, run it\x1b[0m\n" +
	"  2. No, and tell Codex what to do\n"

// An agent running its tests in a pane of their own with the omaterm
// command, for the cli clip: before the split, and after it reads the
// pane.
const (
	siteAgentSplit = "\x1b[1;36m❯\x1b[0m fix the failing theme test\n\n" +
		"\x1b[32m●\x1b[0m Read \x1b[1minternal/theme/omarchy_test.go\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Update \x1b[1minternal/theme/omarchy.go\x1b[0m  \x1b[32m+3\x1b[0m \x1b[31m-1\x1b[0m\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm split --down -- go test ./internal/theme/...\n" +
		"  \x1b[2m└\x1b[0m 7f3k2mq\n"
	siteAgentRead = "\x1b[32m●\x1b[0m Bash   omaterm wait 7f3k2mq --exit\n" +
		"  \x1b[2m└\x1b[0m exited 0\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm read 7f3k2mq\n" +
		"  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m  omaterm/internal/theme  0.214s\n\n" +
		"Fixed. The light flag was read before the theme loaded;\n" +
		"the tests pass in the pane below.\n\n" +
		"\x1b[1;36m❯\x1b[0m "
)

// An agent at work on a branch of its own, for the worktree clip.
const siteWorktreeAgent = "\x1b[1;36m❯\x1b[0m fix the login redirect after sign-in\n\n" +
	"\x1b[32m●\x1b[0m Read \x1b[1minternal/auth/redirect.go\x1b[0m\n" +
	"\x1b[32m●\x1b[0m Update \x1b[1minternal/auth/redirect.go\x1b[0m  \x1b[32m+12\x1b[0m \x1b[31m-4\x1b[0m\n" +
	"\x1b[32m●\x1b[0m Update \x1b[1minternal/auth/redirect_test.go\x1b[0m  \x1b[32m+29\x1b[0m \x1b[31m-2\x1b[0m\n" +
	"\x1b[32m●\x1b[0m Bash   go test ./internal/auth/...\n" +
	"  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m  omaterm/internal/auth  0.318s\n" +
	"\x1b[32m●\x1b[0m Bash   git commit -am \"Redirect to the page the user came from\"\n" +
	"  \x1b[2m└\x1b[0m [fix-login 4e1f2a0] Redirect to the page the user came from\n\n" +
	"Done: after sign-in, the user lands on the page they came from.\n" +
	"The branch is fix-login; main is untouched.\n\n" +
	"\x1b[1;36m❯\x1b[0m "

// A lead agent handing its workers their parts, a worker taking its
// message, and the answer coming back to the lead, for the team clip.
const (
	siteLeadScreen = "\x1b[1;36m❯\x1b[0m split the stats work: the parser and the report\n\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm new --worktree w1 -- codex\n" +
		"  \x1b[2m└\x1b[0m 8e4buc2\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm new --worktree w2 -- opencode\n" +
		"  \x1b[2m└\x1b[0m kzvkbjj\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm ask w1 \"Write the parser in stats/parse.go, with tests, and commit\" --no-wait\n" +
		"  \x1b[2m└\x1b[0m m1\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm ask w2 \"Write the report in stats/report.go, with tests, and commit\" --no-wait\n" +
		"  \x1b[2m└\x1b[0m m2\n\n" +
		"Both workers have their tasks. Their answers will wake me.\n\n" +
		"\x1b[1;36m❯\x1b[0m "
	siteWorkerScreen = "\x1b[1m›\x1b[0m [omaterm] Message m1 from the agent in session \"lead\" (pane 3tumy58), not from the user:\n" +
		"  Write the parser in stats/parse.go, with tests, and commit\n\n" +
		"  Answer it with: omaterm reply m1 \"…\" (or --file <path>)\n\n" +
		"\x1b[2m•\x1b[0m Added \x1b[1mstats/parse.go\x1b[0m \x1b[32m+88\x1b[0m\n" +
		"\x1b[2m•\x1b[0m Added \x1b[1mstats/parse_test.go\x1b[0m \x1b[32m+41\x1b[0m\n" +
		"\x1b[2m•\x1b[0m Ran \x1b[1mgo test ./stats/...\x1b[0m\n" +
		"  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m\n" +
		"\x1b[2m•\x1b[0m Ran \x1b[1mgit commit -am \"Parse the stats input\"\x1b[0m\n" +
		"\x1b[2m•\x1b[0m Ran \x1b[1momaterm reply m1 \"Done: stats/parse.go parses the input; 4 tests pass, committed on w1\"\x1b[0m\n" +
		"  \x1b[2m└\x1b[0m answered m1 from lead\n"
	siteLeadAnswer = "\x1b[1;36m❯\x1b[0m [omaterm] w1's answer to your message m1:\n" +
		"  Done: stats/parse.go parses the input; 4 tests pass, committed on w1\n\n" +
		"\x1b[32m●\x1b[0m Bash   omaterm merge w1\n" +
		"  \x1b[2m└\x1b[0m merged w1 into feat: 1 commit, 2 files; w1 removed\n\n" +
		"w1 is merged into feat. w2 is still on the report; I'll merge it\n" +
		"when its answer comes.\n\n" +
		"\x1b[1;36m❯\x1b[0m "
)

// videoRec renders the app into numbered frames.
type videoRec struct {
	t        *testing.T
	tr       *ui.Tester
	dir      string
	n        int
	last     *image.RGBA
	captions []videoCaption
	enc      png.Encoder
}

// secs is the time of the next frame.
func (r *videoRec) secs() float64 { return float64(r.n) / videoFPS }

func (r *videoRec) write(img *image.RGBA) {
	var buf bytes.Buffer
	if err := r.enc.Encode(&buf, img); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("%05d.png", r.n)), buf.Bytes(), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.n++
}

// shoot renders the app as it is now and copies the picture.
func (r *videoRec) shoot() *image.RGBA {
	r.tr.Advance(time.Second / videoFPS)
	r.tr.Frame()
	src := r.tr.Image()
	img := image.NewRGBA(src.Rect)
	copy(img.Pix, src.Pix)
	return img
}

// play renders n frames, letting spinners and timers run.
func (r *videoRec) play(n int) {
	for range n {
		r.last = r.shoot()
		r.write(r.last)
	}
}

// hold repeats a still picture for n frames.
func (r *videoRec) hold(img *image.RGBA, n int) {
	r.last = img
	for range n {
		r.write(img)
	}
}

// fade crossfades from the last frame to the app as it is now.
func (r *videoRec) fade(n int) { r.fadeTo(r.shoot(), n) }

// fadeTo crossfades from the last frame to img.
func (r *videoRec) fadeTo(img *image.RGBA, n int) {
	from := r.last
	mix := image.NewRGBA(img.Rect)
	for i := 1; i <= n; i++ {
		k := uint32(i * 256 / (n + 1))
		for p := range mix.Pix {
			mix.Pix[p] = uint8((uint32(from.Pix[p])*(256-k) + uint32(img.Pix[p])*k) >> 8)
		}
		r.write(mix)
	}
	r.last = img
	r.write(img)
}

// until plays frames until the scene that began at frame start is secs long.
func (r *videoRec) until(start int, secs float64) {
	r.play(max(0, int(secs*videoFPS)-(r.n-start)))
}

// stream feeds s a few runes per frame, as an agent prints.
func (r *videoRec) stream(feed func(string), s string, perFrame int) {
	runes := []rune(strings.ReplaceAll(s, "\n", "\r\n"))
	for i := 0; i < len(runes); {
		j := min(i+perFrame, len(runes))
		// Keep an escape sequence in one piece.
		for j < len(runes) && !endsSGR(runes[i:j]) {
			j++
		}
		feed(string(runes[i:j]))
		i = j
		r.play(1)
	}
}

// endsSGR reports whether every escape sequence in rs is complete.
func endsSGR(rs []rune) bool {
	open := false
	for _, c := range rs {
		switch {
		case c == '\x1b':
			open = true
		case open && c == 'm':
			open = false
		}
	}
	return !open
}

// lines feeds s a line at a time, every frames frames.
func (r *videoRec) lines(feed func(string), s string, frames int) {
	for _, l := range strings.SplitAfter(s, "\n") {
		if l == "" {
			continue
		}
		feed(strings.ReplaceAll(l, "\n", "\r\n"))
		r.play(frames)
	}
}

// A videoCaption shows from frame at until the next one, or the end.
type videoCaption struct {
	at   int
	text string
}

// caption shows text from now on.
func (r *videoRec) caption(text string) {
	r.captions = append(r.captions, videoCaption{r.n, text})
}

// captionsFile lists the captions as captions.txt has them.
func (r *videoRec) captionsFile() []byte {
	var b strings.Builder
	for i, c := range r.captions {
		end := r.n
		if i+1 < len(r.captions) {
			end = r.captions[i+1].at
		}
		fmt.Fprintf(&b, "%.3f %.3f %s\n", float64(c.at)/videoFPS, float64(end)/videoFPS, c.text)
	}
	return []byte(b.String())
}

// videoScene is the app the clips play out in: an agent, a dev server and
// a test run split in the first tab, and two more sessions beside it.
type videoScene struct {
	t     *testing.T
	a     *appState
	tab   *topTab
	ids   []string
	feeds []func(string)
	tall  bool
	wd    string
	since int64
}

func newVideoScene(t *testing.T, wd string, tall bool) *videoScene {
	a := newShotApp(wd)
	shotStatuses(a) // the other sessions' statuses
	tab := &a.tabs[a.activeTab]
	if tall {
		// Stacked: side by side, the lower panes would wrap their lines.
		tab.Split = &splitNode{
			Dir: splitHorizontal, Ratio: 50,
			First: &splitNode{TermID: randomTermID()},
			Second: &splitNode{Dir: splitHorizontal, Ratio: 45,
				First:  &splitNode{TermID: randomTermID()},
				Second: &splitNode{TermID: randomTermID()},
			},
		}
	} else {
		tab.Split = &splitNode{
			Dir: splitVertical, Ratio: 55,
			First: &splitNode{TermID: randomTermID()},
			Second: &splitNode{Dir: splitHorizontal, Ratio: 50,
				First:  &splitNode{TermID: randomTermID()},
				Second: &splitNode{TermID: randomTermID()},
			},
		}
	}
	a.inspectorVisible = false
	v := &videoScene{t: t, a: a, tab: tab, ids: tab.Split.termIDs(), tall: tall, wd: wd,
		since: nowFunc().Add(-2 * time.Minute).UnixMilli()}
	v.status(0, session.Info{Status: session.Working, Agent: "claude", Process: "claude"})
	v.status(1, session.Info{Status: session.Working, Process: "bun", Title: "vite"})
	v.status(2, session.Info{Status: session.Working, Process: "go"})
	a.activePaneTermID = v.ids[0]
	return v
}

// status sets the status of the first tab's pane i.
func (v *videoScene) status(i int, info session.Info) {
	info.ID, info.Session, info.Since = v.ids[i], v.tab.ID, v.since
	info.Dir = "/home/you/Projects/omaterm"
	if info.Progress == 0 {
		info.Progress = -1
	}
	v.a.status[v.ids[i]] = info
}

// feed returns a writer into pane id's terminal, starting it.
func (v *videoScene) feed(id string) func(string) {
	term := v.a.term(id)
	if term == nil {
		v.t.Fatalf("pane %s did not start", id)
	}
	return func(s string) { term.Feed([]byte(s)) }
}

// start renders the first frame, which starts the panes.
func (v *videoScene) start(r *videoRec) {
	r.tr.Frame()
	for _, id := range v.ids {
		v.feeds = append(v.feeds, v.feed(id))
	}
}

// work plays the three panes at work: the agent reads, the dev server is
// up, and the tests run and pass.
func (v *videoScene) work(r *videoRec) {
	prompt, rest, _ := strings.Cut(siteAgentWaiting, "\n\n")
	reads, _, _ := strings.Cut(rest, "\n\n")
	_, testOut, _ := strings.Cut(siteTests, "./...\n")
	v.feeds[1](strings.ReplaceAll(siteDevServer, "\n", "\r\n"))
	v.feeds[2]("\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[32m❯\x1b[0m ")
	r.play(10)
	r.stream(v.feeds[0], prompt+"\n\n", 2)
	r.stream(v.feeds[2], "go test -run Theme ./...\n", 3)
	work := strings.SplitAfter(reads+"\n\n", "\n")
	tests := strings.SplitAfter(testOut, "\n")
	for i := range max(len(work), len(tests)) {
		if i < len(work) {
			v.feeds[0](strings.ReplaceAll(work[i], "\n", "\r\n"))
		}
		if i < len(tests) {
			v.feeds[2](strings.ReplaceAll(tests[i], "\n", "\r\n"))
		}
		r.play(10)
	}
	v.status(2, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed", Unread: true})
}

// ask has the agent ask for permission.
func (v *videoScene) ask(r *videoRec) {
	_, ask, _ := strings.Cut(siteAgentWaiting, "\n\n\x1b[33m╭")
	r.lines(v.feeds[0], "\x1b[33m╭"+ask, 3)
	v.status(0, session.Info{Status: session.Waiting, Agent: "claude", Process: "claude", Kind: "permission", Unread: true})
}

// finish has the agent finish its turn, zoomed, then shows Last turn.
func (v *videoScene) finish(r *videoRec, lastTurn func()) {
	v.status(0, session.Info{Status: session.Working, Agent: "claude", Process: "claude"})
	v.feeds[0]("\x1b[2J\x1b[H")
	v.tab.Zoomed = v.ids[0]
	// A tall window has no room for the sidebar beside Last turn.
	v.a.sidebarVisible = !v.tall
	r.fade(8)
	r.stream(v.feeds[0], siteAgentDone, 6)
	r.play(15)
	lastTurn()
	shotChanges(v.a)
	v.a.changes.root = v.wd
	r.fade(8)
}

// phone frames a phone screenshot, centered on the video's background.
func phone(t *testing.T, size image.Rectangle, shot string) *image.RGBA {
	f, err := os.Open(filepath.Join("site", "assets", shot))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(size)
	bg := color.RGBA{0x0d, 0x11, 0x17, 0xff}
	xdraw.Draw(img, size, &image.Uniform{bg}, image.Point{}, xdraw.Src)
	h := size.Dy() * 92 / 100
	w := h * src.Bounds().Dx() / src.Bounds().Dy()
	const bezel = 14
	body := image.Rect((size.Dx()-w)/2-bezel, (size.Dy()-h)/2-bezel, (size.Dx()+w)/2+bezel, (size.Dy()+h)/2+bezel)
	screen := body.Inset(bezel)
	scaled := image.NewRGBA(screen)
	xdraw.CatmullRom.Scale(scaled, screen, src, src.Bounds(), xdraw.Src, nil)
	roundFill(img, body, 64, func(x, y int) color.RGBA { return color.RGBA{0x2b, 0x31, 0x3b, 0xff} })
	roundFill(img, screen, 52, func(x, y int) color.RGBA { return scaled.RGBAAt(x, y) })
	return img
}

// roundFill paints a rounded rectangle with colors from at, with a
// one-pixel soft edge.
func roundFill(img *image.RGBA, r image.Rectangle, radius int, at func(x, y int) color.RGBA) {
	rad := float64(radius)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cx := min(max(float64(x)+0.5, float64(r.Min.X)+rad), float64(r.Max.X)-rad)
			cy := min(max(float64(y)+0.5, float64(r.Min.Y)+rad), float64(r.Max.Y)-rad)
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := rad - math.Hypot(dx, dy)
			if d <= 0 {
				continue
			}
			k := min(d, 1)
			c, under := at(x, y), img.RGBAAt(x, y)
			blend := func(a, b uint8) uint8 { return uint8(float64(a)*k + float64(b)*(1-k)) }
			img.SetRGBA(x, y, color.RGBA{blend(c.R, under.R), blend(c.G, under.G), blend(c.B, under.B), 0xff})
		}
	}
}

func TestSiteVideo(t *testing.T) {
	clip, dir := os.Getenv("SITE_VIDEO"), os.Getenv("SITE_VIDEO_DIR")
	if clip == "" || dir == "" {
		t.Skip("run scripts/site-video.sh to render the promo videos")
	}
	format, ok := videoFormats[os.Getenv("SITE_VIDEO_FORMAT")]
	if !ok {
		t.Fatalf("SITE_VIDEO_FORMAT is %q, not wide, tall or site", os.Getenv("SITE_VIDEO_FORMAT"))
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	v := newVideoScene(t, wd, os.Getenv("SITE_VIDEO_FORMAT") == "tall")
	defer stopTermPanes(v.a)
	a := v.a
	// The website shows the app at its own size: a bigger terminal and
	// editor font reads there. Applied, not saved.
	if os.Getenv("SITE_VIDEO_FORMAT") == "site" {
		a.fontSize = defaultFontSize + 2
		a.applyTextSettings()
	}
	tr := ui.NewTester(a.view, format.w, format.h)
	tr.SetScale(format.scale)
	r := &videoRec{t: t, tr: tr, dir: dir, enc: png.Encoder{CompressionLevel: png.BestSpeed}}
	v.start(r)

	switch clip {
	case "launch": // the launch post's overview
		r.caption("3 agent, | satu jendela.")
		v.work(r)
		r.until(0, 6)

		start := r.n
		r.caption("Langsung tahu mana | yang nungguin kamu.")
		v.ask(r)
		r.until(start, 5)

		start = r.n
		r.caption("Lihat apa yang diubah agent | di giliran terakhirnya.")
		v.finish(r, func() {})
		r.until(start, 7)

		start = r.n
		r.caption("Gelap, terang, | atau ikut tema desktop.")
		v.tab.Zoomed = ""
		a.inspectorVisible, a.sidebarVisible = false, true
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Unread: true})
		a.applyTheme(shotTheme(t, "tokyo-night"))
		r.fade(10)
		r.play(2*videoFPS - 11)
		a.applyTheme(shotTheme(t, "lupine"))
		r.fade(10)
		r.until(start, 5)

	case "status": // sessions' statuses, and going to the one that needs you
		r.caption("Tiap sesi kelihatan | statusnya.")
		v.work(r)
		r.until(0, 4)

		start := r.n
		r.caption("Agent butuh izin? | Langsung kelihatan.")
		v.ask(r)
		r.until(start, 3.5)

		start = r.n
		r.caption("Ctrl+Shift+J: lompat ke | sesi yang nungguin kamu.")
		codex := a.tabs[2].Split.termIDs()[0]
		v.feed(codex)(strings.ReplaceAll(siteCodexWaiting, "\n", "\r\n"))
		info := a.status[codex]
		info.Status, info.Kind, info.Unread, info.Progress = session.Waiting, "permission", true, -1
		a.status[codex] = info
		r.play(25)
		a.activateTab(2)
		r.fade(8)
		r.until(start, 4.5)

	case "lastturn": // what the agent's last turn changed
		r.caption("Agent selesai | ngerjain tugasnya.")
		v.work(r)
		r.play(10)
		v.finish(r, func() { r.caption("Last turn: cuma yang diubah | di giliran terakhirnya.") })
		r.play(4 * videoFPS)

	case "cli": // an agent driving omaterm with the omaterm command
		r.caption("Agent bisa pakai | omaterm sendiri.")
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		delete(a.status, v.ids[1])
		delete(a.status, v.ids[2])
		r.play(10)
		r.lines(v.feeds[0], siteAgentSplit, 12)

		start := r.n
		r.caption("Buka pane, jalanin test, | baca hasilnya.")
		v.tab.Split = &splitNode{Dir: splitHorizontal, Ratio: 55,
			First: &splitNode{TermID: v.ids[0]}, Second: &splitNode{TermID: v.ids[2]}}
		v.status(2, session.Info{Status: session.Working, Process: "go"})
		r.fade(6)
		_, testOut, _ := strings.Cut(siteTests, "\n")
		r.lines(v.feeds[2], "\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[32m❯\x1b[0m go test ./internal/theme/...\n"+testOut, 9)
		v.status(2, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed", Unread: true})
		r.until(start, 3.5)

		r.caption("Kamu tinggal nonton.")
		r.lines(v.feeds[0], siteAgentRead, 8)
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Unread: true})
		r.play(int(2.5 * videoFPS))

	case "worktree": // an agent on a branch of its own, merged from the session's menu
		// One idle shell session, and the New Agent dialog, in a worktree,
		// filled in over it.
		a.tabs = a.tabs[:1]
		v.tab = &a.tabs[0]
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		delete(a.status, v.ids[1])
		delete(a.status, v.ids[2])
		v.status(0, session.Info{Status: session.Idle})
		v.feeds[0]("\x1b[1;34m~/Projects/omaterm\x1b[0m \x1b[35mmain\x1b[0m \x1b[32m❯\x1b[0m ")
		r.caption("Agent di branch-nya | sendiri.")
		r.play(12)
		a.wtNew = &worktreeNew{open: true, project: a.projects[0].ID, name: "omaterm", dir: v.wd, git: true, worktree: true,
			branch: "fix-login", base: "main", bases: []string{"main", "dev"}, agent: "claude", env: true}
		r.fade(8)
		r.until(0, 4)

		// Started: the session shows its branch, and the agent works in
		// its worktree; a second one beside it, on another branch.
		start := r.n
		r.caption("Satu worktree, satu agent: | nggak saling ganggu.")
		a.wtNew = nil
		v.tab.Label = "fix-login"
		v.tab.Worktree = v.worktree("fix-login", "main")
		v.feeds[0]("\x1b[2J\x1b[H")
		v.status(0, session.Info{Status: session.Working, Agent: "claude", Process: "claude"})
		other := v.session("new-ui", "", v.worktree("new-ui", "main"),
			session.Info{Status: session.Working, Agent: "codex", Process: "codex", Progress: 40})
		r.fade(8)
		r.lines(v.feeds[0], siteWorktreeAgent, 7)
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Unread: true,
			Summary: "After sign-in, the user lands on the page they came from."})
		r.until(start, 6.5)

		// Merge into main, from the session's menu.
		start = r.n
		r.caption("Selesai? Merge ke main | dari menu sesi.")
		a.wtMerge = &worktreeMerge{open: true, session: v.tab.ID, w: *v.tab.Worktree, how: "squash", remove: true,
			msg: "Fix the login redirect after sign-in",
			sum: &worktree.Summary{Commits: []string{"Redirect to the page the user came from", "Test the redirect"}, Files: 2, Added: 41, Removed: 6}}
		r.fade(8)
		r.until(start, 4)

		start = r.n
		r.caption("Di-merge, worktree-nya dibersihkan. | main tetap rapi.")
		a.wtMerge = nil
		a.toast(nil, "fix-login merged into main")
		a.tabs = append(a.tabs[:0], a.tabs[1:]...)
		a.activeTab = 0
		v.tab = &a.tabs[0]
		_ = other
		r.fade(8)
		r.until(start, 3.5)

	case "team": // a lead agent, its workers, and a reviewer
		// The lead alone, on its branch, at work.
		a.tabs = a.tabs[:1]
		v.tab = &a.tabs[0]
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		v.tab.Label = "lead"
		v.tab.Worktree = v.worktree("feat", "main")
		delete(a.status, v.ids[1])
		delete(a.status, v.ids[2])
		v.status(0, session.Info{Status: session.Working, Agent: "claude", Process: "claude"})
		r.caption("Satu agent jadi lead, | yang lain pekerja.")
		r.play(10)
		first, rest, _ := strings.Cut(siteLeadScreen, "  \x1b[2m└\x1b[0m kzvkbjj\n")
		r.lines(v.feeds[0], first+"  \x1b[2m└\x1b[0m kzvkbjj\n", 7)
		// The workers it started nest under it.
		w1 := v.session("w1", v.tab.ID, v.worktree("w1", "feat"),
			session.Info{Status: session.Idle, Agent: "codex", Process: "codex"})
		w2 := v.session("w2", v.tab.ID, v.worktree("w2", "feat"),
			session.Info{Status: session.Idle, Agent: "opencode", Process: "opencode"})
		r.fade(8)
		r.lines(v.feeds[0], rest, 7)
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Asking: w1.Split.TermID})
		v.statusOf(w1, session.Info{Status: session.Working, Agent: "codex", Process: "codex", Progress: 30})
		v.statusOf(w2, session.Info{Status: session.Working, Agent: "opencode", Process: "opencode"})
		r.until(0, 7)

		// The message, typed into the worker's prompt.
		start := r.n
		r.caption("Pesannya masuk ke prompt | pekerja begitu dia siap.")
		a.activateTab(1)
		r.fade(8)
		r.lines(v.feed(w1.Split.TermID), siteWorkerScreen, 7)
		v.statusOf(w1, session.Info{Status: session.Idle, Agent: "codex", Process: "codex", Unread: true,
			Summary: "Done: stats/parse.go parses the input; 4 tests pass, committed on w1"})
		r.until(start, 6)

		// The answer wakes the lead, which merges the worker.
		start = r.n
		r.caption("Jawabannya balik ke lead | dan membangunkannya.")
		a.activateTab(0)
		v.feeds[0]("\x1b[2J\x1b[H")
		v.status(0, session.Info{Status: session.Working, Agent: "claude", Process: "claude"})
		r.fade(8)
		r.lines(v.feeds[0], siteLeadAnswer, 7)
		a.tabs = append(a.tabs[:1], a.tabs[2:]...) // w1 merged and gone
		v.tab = &a.tabs[0]
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Unread: true,
			Summary: "w1 is merged into feat; waiting for w2's report."})
		r.fade(8)
		r.until(start, 6)

		// Ask for Review: another agent reads the work and sends back
		// what to fix; its verdict shows under its session.
		start = r.n
		r.caption("Ask for Review: agent lain | me-review kerjaannya.")
		a.openReviewDialog(*v.tab)
		r.fade(8)
		r.until(start, 3.5)
		start = r.n
		a.review = nil
		rev := v.session("review lead", v.tab.ID, nil,
			session.Info{Status: session.Idle, Agent: "codex", Process: "codex", Unread: true,
				Summary: "feat approved after 2 rounds: the parser and the report hold up, tests cover the edge cases"})
		_ = rev
		r.fade(8)
		r.until(start, 4)

	case "resume": // sessions outlive the window and a reboot
		r.caption("Tutup jendelanya...")
		v.feeds[0](strings.ReplaceAll(siteAgentDone, "\n", "\r\n"))
		v.feeds[1](strings.ReplaceAll(siteDevServer, "\n", "\r\n"))
		v.feeds[2](strings.ReplaceAll(siteTests, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude", Unread: true})
		v.status(2, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed", Unread: true})
		r.play(int(2.5 * videoFPS))

		// Uncaptioned, on the website, a dark screen in a loop looks broken:
		// there the sessions go straight to how they come back.
		if os.Getenv("SITE_VIDEO_FORMAT") != "site" {
			r.caption("...bahkan reboot.")
			dark := image.NewRGBA(r.last.Rect)
			xdraw.Draw(dark, dark.Rect, &image.Uniform{color.RGBA{0x0d, 0x11, 0x17, 0xff}}, image.Point{}, xdraw.Src)
			r.fadeTo(dark, 12)
			r.hold(dark, int(1.6*videoFPS))
		}

		start := r.n
		r.caption("Sesi balik, agent lanjut | percakapan yang sama.")
		// What a resumed Claude Code shows: the conversation it picks up.
		for i, f := range v.feeds {
			f("\x1b[2J\x1b[H")
			v.status(i, session.Info{Status: session.Working, Process: []string{"claude", "bun", "go"}[i]})
		}
		v.feeds[0]("\x1b[2m  Resuming conversation…\x1b[0m\r\n\r\n")
		r.fadeTo(r.shoot(), 12)
		r.lines(v.feeds[0], siteAgentDone, 3)
		v.feeds[1](strings.ReplaceAll(siteDevServer, "\n", "\r\n"))
		v.feeds[2](strings.ReplaceAll(siteTests, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude"})
		v.status(1, session.Info{Status: session.Working, Process: "bun", Title: "vite"})
		v.status(2, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed"})
		r.until(start, 4.5)

	case "allinone": // the Git panel, graph, diff, editor and palette
		// A tall window has no room for the sidebar beside the panels.
		a.sidebarVisible = !v.tall
		a.inspectorVisible, a.inspectorTab = true, 2
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		v.feeds[0](strings.ReplaceAll(siteShell, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Process: "zsh"})
		r.caption("Nggak perlu pindah aplikasi: | Git panel.")
		r.play(int(2.6 * videoFPS))

		views := []struct {
			caption string
			show    func()
		}{
			// Tall, the graph and the diff need the inspector's room.
			{"Commit graph.", func() {
				a.inspectorVisible = !v.tall
				a.activeView = tabGraph
				a.commits = shotMergeCommits()
				a.graphLoaded = true
			}},
			{"Diff berdampingan.", func() {
				a.setDiffBuffers(
					"package main\n\nfunc greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"world\"))\n}\n",
					"package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"omaterm\"))\n}\n",
					"main.go")
				a.diffTitle = "main.go · Index → Working Tree"
				a.diffSrc = diffSource{Kind: diffWorking, Path: "main.go"}
				a.focusDiffTab("main.go")
			}},
			{"Editor.", func() { a.inspectorVisible, a.inspectorTab = true, 1; a.openEditor("app.go") }},
			{"Command palette.", func() { a.openOverlay(overlayCommandPalette) }},
		}
		for _, view := range views {
			r.caption(view.caption)
			view.show()
			r.fade(8)
			r.play(int(2.3*videoFPS) - 9)
		}
		r.play(15)

	case "splits": // sessions and split panes from the keyboard
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		for i := 1; i < len(v.ids); i++ {
			delete(a.status, v.ids[i])
		}
		v.feeds[0](strings.ReplaceAll(siteAgentDone, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude"})
		r.caption("Satu sesi, | satu pane.")
		r.play(int(1.5 * videoFPS))

		// Tall, the first split goes down: side by side, both panes would
		// wrap their lines.
		splits := []struct {
			caption string
			dir     splitDir
		}{{"Ctrl+Shift+R | split ke kanan.", splitVertical}, {"Ctrl+Shift+D | split ke bawah.", splitHorizontal}}
		if v.tall {
			splits[0], splits[1] = splits[1], splits[0]
		}
		at := v.ids[0]
		for i, sp := range splits {
			r.caption(sp.caption)
			at = a.splitPane(at, sp.dir)
			r.fade(6)
			if i == 0 {
				r.lines(v.feed(at), siteDevServer, 4)
				v.paneStatus(at, session.Info{Status: session.Working, Process: "bun", Title: "vite"})
			} else {
				r.lines(v.feed(at), siteTests, 4)
				v.paneStatus(at, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed"})
			}
			r.play(20)
		}

		r.caption("Ctrl+Shift+Enter | zoom satu pane.")
		v.tab.Zoomed = v.ids[0]
		r.fade(8)
		r.play(int(1.4 * videoFPS))
		v.tab.Zoomed = ""
		r.fade(8)
		r.play(15)

		r.caption("Ctrl+T | sesi baru.")
		a.newTerminalTab()
		r.fade(8)
		r.play(int(1.8 * videoFPS))

	case "files": // go to a file, search the project
		a.inspectorVisible, a.inspectorTab = !v.tall, 1
		a.sidebarVisible = !v.tall
		v.tab.Split = &splitNode{TermID: v.ids[0]}
		v.feeds[0](strings.ReplaceAll(siteShell, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Process: "zsh"})
		out, err := exec.Command("git", "ls-files").Output()
		if err != nil {
			t.Fatal(err)
		}
		a.fileIndex = strings.Fields(string(out))
		r.play(10)

		r.caption("Ctrl+P | buka file apa aja.")
		a.openOverlay(overlayQuickOpen)
		a.fileIndex = strings.Fields(string(out))
		r.fade(6)
		for _, q := range []string{"t", "th", "the", "them", "theme", "themew"} {
			a.quickQuery = q
			r.play(5)
		}
		r.play(15)
		a.closeOverlay()
		a.openEditor("themewatch.go")
		r.fade(8)
		r.play(int(1.2 * videoFPS))

		r.caption("Ctrl+Shift+F | cari di seluruh project.")
		a.openOverlay(overlayGlobalSearch)
		r.fade(6)
		// The matches follow the query as it is typed, as a search does.
		const query = "applyTheme"
		for i := 4; i <= len(query); i += 2 {
			a.searchQuery = query[:i]
			searchVideo(t, a, query[:i])
			r.play(4)
		}
		r.play(int(2.5 * videoFPS))

	case "ports": // what the sessions run and the ports they open
		a.inspectorVisible, a.inspectorTab = true, 0
		a.sidebarVisible = !v.tall
		v.tab.Split = &splitNode{Dir: splitHorizontal, Ratio: 50,
			First: &splitNode{TermID: v.ids[0]}, Second: &splitNode{TermID: v.ids[1]}}
		v.feeds[0](strings.ReplaceAll(siteAgentDone, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Idle, Agent: "claude", Process: "claude"})
		v.status(1, session.Info{Status: session.Idle, Process: "zsh"})
		a.processes = []service.Process{{Name: "claude", Pid: 48211, CPU: "1.2%", Mem: "212 MB"}}
		a.ports = nil
		r.caption("Panel Info: proses | tiap sesi.")
		r.play(int(1.5 * videoFPS))

		v.feeds[1]("\x1b[2m$\x1b[0m ")
		r.stream(v.feeds[1], "bun run dev\n", 1)
		v.status(1, session.Info{Status: session.Working, Process: "bun", Title: "vite"})
		a.processes = append(a.processes, service.Process{Name: "bun", Pid: 48305, CPU: "4.1%", Mem: "96 MB"})
		_, devOut, _ := strings.Cut(siteDevServer, "bun run dev\n")
		r.lines(v.feeds[1], devOut, 5)
		r.play(10)

		r.caption("Dev server buka port? | Langsung kelihatan.")
		a.ports = []service.Port{{Port: 5173, Name: "http", Open: true, Pid: 48305, Process: "bun"}}
		a.processes[1].CPU = "0.8%"
		r.fade(6)
		r.play(int(3.2 * videoFPS))

	case "settings": // the Settings window
		v.feeds[0](strings.ReplaceAll(siteAgentDone, "\n", "\r\n"))
		v.feeds[1](strings.ReplaceAll(siteDevServer, "\n", "\r\n"))
		v.feeds[2](strings.ReplaceAll(siteTests, "\n", "\r\n"))
		r.play(1)
		back := r.last
		for p := range back.Pix {
			if p%4 != 3 {
				back.Pix[p] = back.Pix[p] * 2 / 5
			}
		}
		a.openSettings("general")
		scale := float32(1.5)
		if v.tall {
			scale = 1.25
		}
		st := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
		st.SetScale(scale)
		st.Frame()
		frame := func() *image.RGBA {
			st.Advance(time.Second / videoFPS)
			st.Frame()
			return overWindow(back, st.Image())
		}
		r.caption("Semua pengaturan | di satu jendela.")
		r.fadeTo(frame(), 8)
		for _, page := range []string{"appearance", "terminal", "agents", "web"} {
			for range 28 {
				r.hold(frame(), 1)
			}
			a.settingsGo(page)
		}
		for range 28 {
			r.hold(frame(), 1)
		}
		r.caption("Cari setting apa aja.")
		a.settingsGo("general")
		for _, q := range []string{"f", "fo", "fon", "font"} {
			a.settingsUI.query = q
			for range 5 {
				r.hold(frame(), 1)
			}
		}
		for range 25 {
			r.hold(frame(), 1)
		}
		// The first result: the terminal's font.
		a.settingsGo("terminal")
		for range int(1.8 * videoFPS) {
			r.hold(frame(), 1)
		}

	case "phone": // web access from a phone
		r.caption("Aktifkan Web Access, | pair HP pakai QR.")
		v.feeds[0](strings.ReplaceAll(siteAgentWaiting, "\n", "\r\n"))
		v.feeds[1](strings.ReplaceAll(siteDevServer, "\n", "\r\n"))
		v.feeds[2](strings.ReplaceAll(siteTests, "\n", "\r\n"))
		v.status(0, session.Info{Status: session.Waiting, Agent: "claude", Process: "claude", Kind: "permission", Unread: true})
		v.status(2, session.Info{Status: session.Idle, Process: "go", Message: "Tests passed", Unread: true})
		r.play(15)
		pairVideo(t, a)
		r.fade(8)
		r.until(0, 3.5)

		size := r.last.Rect
		r.caption("Semua sesi kelihatan | dari HP.")
		r.fadeTo(phone(t, size, "web-phone-list.png"), 10)
		r.hold(r.last, 3*videoFPS-11)

		r.caption("Balas agent | langsung dari sana.")
		r.fadeTo(phone(t, size, "web-phone.png"), 10)
		r.hold(r.last, int(3.5*videoFPS)-11)

	default:
		t.Fatalf("no clip %q: launch, status, cli, lastturn, resume, phone, allinone, splits, files, ports or settings", clip)
	}

	if err := os.WriteFile(filepath.Join(dir, "captions.txt"), r.captionsFile(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d frames, %.1fs", clip, r.n, r.secs())
}

// paneStatus sets the status of a pane split off in the first tab.
func (v *videoScene) paneStatus(id string, info session.Info) {
	info.ID, info.Session, info.Since = id, v.tab.ID, v.since
	info.Dir = "/home/you/Projects/omaterm"
	if info.Progress == 0 {
		info.Progress = -1
	}
	v.a.status[id] = info
}

// session adds a session to the sidebar: a tab of one pane, the child of
// parent when given, in worktree wt when given, with its pane's status.
func (v *videoScene) session(label, parent string, wt *model.Worktree, info session.Info) *topTab {
	tab := topTab{ID: newID(), Kind: tabTerminal, Label: label, Dot: dotGreen, Split: &splitNode{TermID: randomTermID()},
		Created: nowFunc().UnixMilli(), Parent: parent, Worktree: wt}
	v.a.tabs = append(v.a.tabs, tab)
	t := &v.a.tabs[len(v.a.tabs)-1]
	v.statusOf(t, info)
	return t
}

// worktree is a worktree a session of the clip works in: its folder is
// made, so the session doesn't show it as gone.
func (v *videoScene) worktree(branch, base string) *model.Worktree {
	dir := filepath.Join(v.t.TempDir(), ".worktrees", branch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		v.t.Fatal(err)
	}
	return &model.Worktree{Dir: dir, Branch: branch, Base: base}
}

// statusOf sets the status of a session's pane.
func (v *videoScene) statusOf(t *topTab, info session.Info) {
	info.ID, info.Session, info.Since = t.Split.TermID, t.ID, v.since
	info.Dir = "/home/you/Projects/omaterm"
	if info.Progress == 0 {
		info.Progress = -1
	}
	v.a.status[t.Split.TermID] = info
}

// searchVideo fills global search with this repo's matches of query, and
// previews the first.
func searchVideo(t *testing.T, a *appState, query string) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	a.searchResults = nil
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if at := strings.Index(line, query); at >= 0 {
				a.searchResults = append(a.searchResults, service.Match{Path: f, Line: i + 1, Text: line, Start: at, End: at + len(query)})
			}
		}
	}
	if len(a.searchResults) == 0 {
		t.Fatalf("no matches of %q", query)
	}
	a.searchSelected = 0
	m := a.searchResults[0]
	data, _ := os.ReadFile(m.Path)
	st := &editor.State{ReadOnly: true, Theme: omaEditorTheme(), Font: a.editorFont(a.fontSize)}
	st.Buffer = editor.NewBuffer(string(data))
	st.Buffer.SetLanguage(editor.LanguageFor(m.Path))
	st.GoTo(editor.Pos{Line: m.Line - 1, Col: 0})
	st.Select(st.Buffer.Offset(editor.Pos{Line: m.Line - 1, Col: m.Start}), st.Buffer.Offset(editor.Pos{Line: m.Line - 1, Col: m.End}))
	a.searchPreview = st
}

// overWindow draws a window's picture, with round corners, in the middle
// of back.
func overWindow(back, win *image.RGBA) *image.RGBA {
	img := image.NewRGBA(back.Rect)
	copy(img.Pix, back.Pix)
	w, h := win.Rect.Dx(), win.Rect.Dy()
	at := image.Rect((back.Rect.Dx()-w)/2, (back.Rect.Dy()-h)/2, (back.Rect.Dx()+w)/2, (back.Rect.Dy()+h)/2)
	edge := at.Inset(-1)
	roundFill(img, edge, 15, func(x, y int) color.RGBA { return color.RGBA{0x3a, 0x41, 0x4d, 0xff} })
	roundFill(img, at, 14, func(x, y int) color.RGBA { return win.RGBAAt(x-at.Min.X, y-at.Min.Y) })
	return img
}

// pairVideo opens Pair a Device, as the pair site shot does.
func pairVideo(t *testing.T, a *appState) {
	link := "https://box.tailnet.ts.net/#code=K7QM-4XWD"
	rows, err := web.QR(link)
	if err != nil {
		t.Fatal(err)
	}
	open := true
	a.webOn, a.webPairOpen = true, &open
	a.webPair = &webPairing{ready: true, link: link, code: "K7QM-4XWD", qr: rows,
		expires: nowFunc().Add(10 * time.Minute)}
}

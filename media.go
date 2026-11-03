package main

// Media in the editor: a PDF, a song or a video opens in an editor tab
// that holds the system's web view, as browser panes do. The page comes
// from filesrv.go, which serves only the files open so.

import (
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/theme"
	"omaterm/service"
)

// mediaType is a format the web view plays or shows: its kind, and the
// Content-Type it is served with (the system's MIME table lacks some).
type mediaType struct{ kind, mime string }

// mediaTypes are the formats the editor previews.
var mediaTypes = map[string]mediaType{
	".pdf":  {"pdf", "application/pdf"},
	".mp3":  {"audio", "audio/mpeg"},
	".wav":  {"audio", "audio/wav"},
	".ogg":  {"audio", "audio/ogg"},
	".oga":  {"audio", "audio/ogg"},
	".m4a":  {"audio", "audio/mp4"},
	".aac":  {"audio", "audio/aac"},
	".flac": {"audio", "audio/flac"},
	".opus": {"audio", "audio/ogg"},
	".mp4":  {"video", "video/mp4"},
	".webm": {"video", "video/webm"},
	".mov":  {"video", "video/quicktime"},
	".m4v":  {"video", "video/mp4"},
	".ogv":  {"video", "video/ogg"},
}

// mediaKind tells a file the editor previews: "pdf", "audio" or "video",
// by its extension, or "" for any other.
func mediaKind(path string) string {
	return mediaTypes[lowerExt(path)].kind
}

// mediaKindLabel names a kind in the status line.
func mediaKindLabel(kind string) string {
	switch kind {
	case "pdf":
		return "PDF"
	case "audio":
		return "Audio"
	}
	return "Video"
}

// openMedia opens a media file without reading it: a 2 GB video is
// streamed to the web view, never held in memory.
func (a *appState) openMedia(path, kind string) *openFile {
	doc := &openFile{Path: path, Media: kind, checked: time.Now()}
	if st, err := os.Stat(a.docPath(path)); err == nil {
		doc.Size, doc.mtime = st.Size(), st.ModTime()
	}
	return doc
}

// ensureMediaWeb makes a media editor's web view, on its file.
func (a *appState) ensureMediaWeb(doc *openFile) {
	if doc.web != nil || doc.webErr != "" || doc.crashed || a.win == nil {
		return
	}
	look := playerLook{
		Background: theme.Hex(pal.Code), Text: theme.Hex(pal.TextSoft), Muted: theme.Hex(pal.TextMuted),
	}
	url, release, err := mediaFiles.serve(a.docPath(doc.Path), doc.Media, look)
	if err != nil {
		doc.webErr = err.Error()
		return
	}
	secret := newID() + newID()
	v, err := a.win.NewWebView(mygo.WebViewOptions{
		BackgroundColor: look.Background,
		Page:            mygo.PageOptions{PreloadScript: pageKeysScript(secret)},
	})
	if err != nil {
		release()
		doc.webErr = err.Error()
		return
	}
	doc.web, doc.mediaURL, doc.release = v, url, release
	page := v.Page()
	// The app's shortcuts, which the page took (webkeys.go): Ctrl+W closes
	// the tab as it does elsewhere.
	page.OnWillNavigate(func(n *mygo.NavigateEvent) {
		if cmd, ok := pageKey(n, secret); ok {
			a.post(func() {
				if doc.web != v {
					return
				}
				a.queuedActions = append(a.queuedActions, cmd)
				doc.yieldKeys = true
			})
			return
		}
		// The page is the file and nothing else: a web link in a PDF opens
		// in the browser rather than in its place. Other schemes pass, as
		// the PDF viewer's own pages.
		if web := strings.HasPrefix(n.URL, "http://") || strings.HasPrefix(n.URL, "https://"); web && !strings.HasPrefix(n.URL, url) {
			n.PreventDefault()
			mygo.Shell.OpenExternal(n.URL)
		}
	})
	// A file that crashes the page crashes it again as it reloads, every
	// few seconds: as WebKitGTK lacking GStreamer's plugins does on a
	// video. The tab says so instead, once.
	page.OnRenderProcessGone(func(string) {
		a.post(func() {
			if doc.web != v {
				return
			}
			doc.dropMedia()
			doc.crashed = true
		})
	})
	if err := page.LoadURL(url); err != nil {
		doc.webErr = err.Error()
	}
}

// dropMedia closes a media editor's web view and stops serving its file.
func (doc *openFile) dropMedia() {
	if doc == nil {
		return
	}
	if doc.web != nil {
		doc.web.Destroy()
		doc.web = nil
	}
	if doc.release != nil {
		doc.release()
		doc.release = nil
	}
}

// checkMedia reloads a media editor's page when its file changed on
// disk, as an agent writing the PDF again does.
func (a *appState) checkMedia(doc *openFile) {
	doc.checked = time.Now()
	st, err := os.Stat(a.docPath(doc.Path))
	if err != nil || st.ModTime().Equal(doc.mtime) && st.Size() == doc.Size {
		return
	}
	doc.Size, doc.mtime = st.Size(), st.ModTime()
	if doc.web != nil && !doc.web.IsDestroyed() {
		doc.web.Page().Reload()
	}
}

// mediaView shows a PDF, a song or a video in the web view, under the
// file's path and a button that opens it where the system would.
func (a *appState) mediaView(c *ui.Context, doc *openFile) ui.Element {
	a.ensureMediaWeb(doc)
	if time.Since(doc.checked) > 2*time.Second {
		a.checkMedia(doc)
	}
	c.After(2 * time.Second)

	openApp := func() {
		if err := mygo.Shell.OpenPath(a.docPath(doc.Path)); err != nil {
			c.Toast("Can't open " + pathBase(doc.Path) + ": " + err.Error())
		}
	}
	return ui.Column(c).Fill().Background(pal.Code).Children(func() {
		a.viewHeader(c, func() {
			headerTitle(c, iconFileText, pal.TextMuted, pathBase(doc.Path), pathDir(doc.Path))
			ui.Spacer(c)
			if doc.web != nil || doc.crashed {
				if a.cmdButton(c, iconRefresh, iconBtn, false).Tooltip("Reload").Clicked() {
					if doc.crashed {
						// Tried once more, as after installing what it lacked.
						doc.crashed = false
					} else {
						doc.web.Page().Reload()
					}
				}
			}
			if a.cmdButton(c, iconExternalLink, iconBtn, false).Tooltip("Open in default app").Clicked() {
				openApp()
			}
		})

		ui.Box(c.Key("media-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			switch {
			case doc.web == nil:
				ui.Column(c).Fill().Center().Gap(8).Padding(24).Children(func() {
					ui.Icon(c, iconFileText).Size(28, 28).TextColor(pal.TextFaint)
					ui.Text(c, "This file can't be previewed here").FontSize(13).FontWeight(600).TextColor(pal.Text)
					// Only Linux lacks a web view of its own.
					msg := "The system's web view did not start."
					switch {
					case doc.crashed && doc.Media != "pdf" && runtime.GOOS == "linux":
						msg = "The web view crashed playing it: install GStreamer's plugins (gst-plugins-good, gst-libav), then Reload."
					case doc.crashed:
						msg = "The web view crashed showing it."
					case runtime.GOOS == "linux":
						msg = "Install webkit2gtk-4.1 to preview PDF, audio and video."
					}
					ui.Text(c, msg).FontSize(12).TextColor(pal.TextMuted).TextAlign(ui.Center)
					if doc.webErr != "" {
						ui.Text(c, doc.webErr).FontSize(11).TextColor(pal.TextFaint).TextAlign(ui.Center)
					}
					if a.cmdButton(c, iconExternalLink, iconBtn, false).Tooltip("Open in default app").Clicked() {
						openApp()
					}
				})
			case doc.yieldKeys:
				// Hidden a frame, the page gives the keyboard back to the
				// window, as a browser pane's does (browser.go).
				doc.yieldKeys = false
				doc.focused = true
				ui.Box(c.Key("media-yield")).Grow(1)
				a.post(func() {})
			default:
				ref := a.handle("media-" + doc.Path)
				ui.WebView(c.Key("media"), doc.web).Grow(1).Bind(ref)
				switch {
				case ref.Focused(c) && !doc.focused:
					doc.focused = true
					doc.web.Focus()
				case !ref.Focused(c):
					doc.focused = false
				}
			}
		})

		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
			ui.Text(c, mediaKindLabel(doc.Media)).FontSize(10).TextColor(pal.TextMuted)
			ui.Text(c, service.FormatBytes(uint64(doc.Size))).FontSize(10).TextColor(pal.TextMuted)
			ui.Spacer(c)
			// A PDF's format is its kind: said once.
			if ext := strings.ToUpper(strings.TrimPrefix(lowerExt(doc.Path), ".")); ext != mediaKindLabel(doc.Media) {
				ui.Text(c, ext).FontSize(10).TextColor(pal.TextMuted)
			}
		})
	})
}

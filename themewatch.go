package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"omaterm/internal/daemon"
	"omaterm/internal/session"
	"omaterm/internal/theme"
	"omaterm/internal/web"
)

// Theme modes: follow the active Omarchy theme, or one of the built-in
// themes whatever Omarchy shows.
const (
	themeOmarchy = "omarchy"
	themeDark    = "dark"
	themeLight   = "light"
)

// systemDark tells whether the desktop is dark. main sets it once the app
// runs; until then, as in tests, it is.
var systemDark = func() bool { return true }

// themeSource is the theme a mode shows: Omarchy's active theme, falling
// back to the built-in dark one when Omarchy has none (another desktop).
func themeSource(mode string) theme.Source {
	switch mode {
	case themeDark:
		return theme.BuiltinDark()
	case themeLight:
		return theme.BuiltinLight()
	case themeSystem:
		if systemDark() {
			return theme.BuiltinDark()
		}
		return theme.BuiltinLight()
	}
	if src, err := theme.LoadOmarchy(); err == nil {
		return src
	}
	return theme.BuiltinDark()
}

// applyTheme makes src the app's theme and recolors what holds a theme of
// its own: the terminals, the editors, the diff and search previews. UI
// thread only.
func (a *appState) applyTheme(src theme.Source) {
	setPalette(src)
	a.panes.Each(func(p session.Handle) {
		if t := p.TermIfOpen(); t != nil {
			t.SetTheme(termTheme, nil)
			t.SetMinimumContrast(a.minimumContrast())
		}
	})
	for _, doc := range a.editors {
		if doc.State != nil {
			doc.State.Theme = editTheme
		}
	}
	if a.searchPreview != nil {
		a.searchPreview.Theme = editTheme
	}
	if a.diffDs != nil {
		a.diffDs.Theme = editTheme
	}
	a.themeStamp = themeNameStamp()
	a.pushWebTheme()
}

// pushWebTheme tells the session daemon the app's look, for the web page
// to take; off the UI thread, as the daemon may be slow to answer.
func (a *appState) pushWebTheme() {
	r, ok := a.panes.(*daemon.Remote)
	if !ok || themeSrc.Background.A == 0 { // no daemon, or no theme yet
		return
	}
	t := web.ThemeOf(themeSrc)
	go func() {
		if err := r.SetWebTheme(t); err != nil {
			log.Printf("web access: sending the theme: %v", err)
		}
	}()
}

// setThemeMode switches between following Omarchy, following the
// desktop, and a built-in theme.
func (a *appState) setThemeMode(mode string) {
	a.themeMode = mode
	a.applyTheme(themeSource(mode))
	a.saveSettings()
	a.invalidateWindows()
}

// hasOmarchy tells whether Omarchy keeps a theme here to follow.
func hasOmarchy() bool {
	st, err := os.Stat(theme.Dir())
	return err == nil && st.IsDir()
}

// themeNameStamp is the mtime of Omarchy's theme.name, which every theme
// switch rewrites: the window checks it as it comes back, in case the
// watch missed a switch.
func themeNameStamp() time.Time {
	st, err := os.Stat(filepath.Join(theme.Dir(), "theme.name"))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// startThemeWatch follows Omarchy's theme switches. omarchy-theme-set
// replaces the theme directory whole, so the watch is on its parent,
// where theme.name and the theme directory come and go.
func (a *appState) startThemeWatch() {
	dir := theme.Dir()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, err := mygo.WatchFiles(ctx, dir, mygo.FileWatchOptions{Debounce: 150 * time.Millisecond})
	if err != nil {
		cancel()
		return
	}
	a.themeWatchStop = func() { cancel(); w.Close() }
	go func() {
		for {
			evs, err := w.Next(ctx)
			if err != nil {
				return
			}
			relevant := false
			for _, ev := range evs {
				if ev.Path == "theme.name" || ev.Path == "theme" || strings.HasPrefix(ev.Path, "theme/") || ev.Path == "." {
					relevant = true
				}
			}
			if !relevant {
				continue
			}
			src, err := theme.LoadOmarchy()
			if err != nil {
				continue // mid-switch: the next event brings the new theme
			}
			a.post(func() {
				if a.themeMode == themeOmarchy {
					a.applyTheme(src)
				}
			})
		}
	}()
}

// recheckTheme reloads the Omarchy theme if theme.name changed while the
// window was away.
func (a *appState) recheckTheme() {
	if a.themeMode != themeOmarchy {
		return
	}
	if s := themeNameStamp(); !s.Equal(a.themeStamp) {
		a.applyTheme(themeSource(themeOmarchy))
	}
}

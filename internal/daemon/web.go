package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/theme"
	"omaterm/internal/web"
)

// webStore is web access's store, beside state.json.
func (d *Daemon) webStore() *web.Store {
	return web.OpenStore(web.StorePath(filepath.Dir(d.stateFile())))
}

// reloadWeb starts, stops or moves the web server as web.json says, and
// tells where it serves.
func (d *Daemon) reloadWeb() (web.Status, error) {
	store := d.webStore()
	cfg, err := store.Config()
	if err != nil {
		return web.Status{Error: err.Error()}, err
	}
	addr := cfg.ListenAddr()
	d.webMu.Lock()
	defer d.webMu.Unlock()
	if d.web != nil && (!cfg.Enabled || d.webAddr != addr) {
		d.web.Close()
		d.web = nil
		log.Printf("web access: stopped")
	}
	if !cfg.Enabled {
		return web.Status{}, nil
	}
	if d.web == nil {
		s, err := web.Listen(addr, d, store)
		if err != nil {
			return web.Status{Addr: addr, Error: err.Error()}, err
		}
		d.web, d.webAddr = s, addr
		log.Printf("web access: http://%s", addr)
	}
	return web.Status{On: true, Addr: d.webAddr}, nil
}

// webThemePath is where the app's last look for the web page is kept,
// beside web.json, so the page looks like the window after a restart too.
func (d *Daemon) webThemePath() string {
	return filepath.Join(filepath.Dir(d.stateFile()), "web-theme.json")
}

// currentWebTheme is the look the page takes: the app's last, else the
// built-in dark one.
func (d *Daemon) currentWebTheme() web.Theme {
	d.webMu.Lock()
	t := d.webTheme
	d.webMu.Unlock()
	if t != nil {
		return *t
	}
	if b, err := os.ReadFile(d.webThemePath()); err == nil {
		var saved web.Theme
		if json.Unmarshal(b, &saved) == nil && len(saved.Tokens) > 0 {
			d.webMu.Lock()
			d.webTheme = &saved
			d.webMu.Unlock()
			return saved
		}
	}
	return web.ThemeOf(theme.BuiltinDark())
}

// setWebTheme keeps the app's look for the page, and tells the pages,
// which fetch it again.
func (d *Daemon) setWebTheme(t web.Theme) {
	d.webMu.Lock()
	same := d.webTheme != nil && d.webTheme.Stamp == t.Stamp && t.Stamp != ""
	d.webTheme = &t
	d.webMu.Unlock()
	if same {
		return
	}
	if b, err := json.Marshal(t); err == nil {
		if err := os.WriteFile(d.webThemePath(), b, 0o600); err != nil {
			log.Printf("web access: keeping the theme: %v", err)
		}
	}
	d.changed()
}

// webServing tells whether web access serves.
func (d *Daemon) webServing() bool {
	d.webMu.Lock()
	defer d.webMu.Unlock()
	return d.web != nil
}

func (d *Daemon) stopWeb() {
	d.webMu.Lock()
	defer d.webMu.Unlock()
	if d.web != nil {
		d.web.Close()
		d.web = nil
	}
}

// ReloadWeb tells the session daemon web access changed, starting the
// daemon when asked and none runs.
func ReloadWeb(start bool) (web.Status, error) {
	c, err := client.Dial(paths.DaemonSocketPath())
	if errors.Is(err, client.ErrUnreachable) && start {
		if err := StartProcess(); err != nil {
			return web.Status{}, err
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if c, err = client.Dial(paths.DaemonSocketPath()); err == nil {
				break
			}
		}
	}
	if err != nil {
		return web.Status{}, err
	}
	defer c.Close()
	var st web.Status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.CallContext(ctx, "web.reload", nil, &st); err != nil {
		return st, err
	}
	if st.Error != "" {
		return st, errors.New(st.Error)
	}
	return st, nil
}

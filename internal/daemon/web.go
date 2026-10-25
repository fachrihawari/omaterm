package daemon

import (
	"errors"
	"log"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/web"
)

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
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.ListenPort()))
	d.webMu.Lock()
	defer d.webMu.Unlock()
	if d.web != nil && (!cfg.Enabled || d.web.Addr() != addr) {
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
		d.web = s
		log.Printf("web access: http://%s", addr)
	}
	return web.Status{On: true, Addr: d.web.Addr()}, nil
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
	if err := c.Call("web.reload", nil, &st); err != nil {
		return st, err
	}
	if st.Error != "" {
		return st, errors.New(st.Error)
	}
	return st, nil
}

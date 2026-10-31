package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/tunnel"
	"omaterm/internal/web"
)

// webCmd looks after web access: omaterm's sessions in a browser,
// served on the loopback for a tunnel to carry.
func webCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	store := web.OpenStore(web.StorePath(paths.ConfigDir()))
	switch sub {
	case "on":
		// Refused before the config is saved, so web access isn't left on
		// with no daemon to serve it.
		if runtime.GOOS == "windows" {
			return daemon.ErrNoDaemon
		}
		f, err := parse(args, map[string]string{"port": "port", "p": "port", "url": "url", "host": "host", "tunnel": "tunnel"},
			map[string]string{"network": "network", "local": "local"})
		if err != nil {
			return err
		}
		via, err := tunnelOf(f)
		if err != nil {
			return err
		}
		port := 0
		if v, ok := f.vals["port"]; ok {
			if port, err = strconv.Atoi(v); err != nil || port <= 0 || port > 65535 {
				return usageErr("--port takes a port number")
			}
		}
		cfg, err := store.SetConfig(func(c *web.Config) {
			c.Enabled = true
			if port != 0 {
				c.Port = port
			}
			switch {
			case f.set["network"]:
				c.Host = "0.0.0.0"
			case f.set["local"]:
				c.Host = ""
			case f.vals["host"] != "":
				c.Host = f.vals["host"]
			}
			if u, ok := f.vals["url"]; ok {
				c.URL = u
			}
			if _, ok := f.vals["tunnel"]; ok {
				c.Tunnel = via
			}
		})
		if err != nil {
			return err
		}
		st, err := daemon.ReloadWeb(true)
		if err != nil {
			return err
		}
		fmt.Printf("web access on: %s (listening on %s)\n", web.BaseURL(cfg, st.TunnelURL), st.Addr)
		sayTunnel(cfg.Tunnel, st)
		if cfg.OnNetwork() {
			fmt.Println(networkWarning)
		}
		if devices, _ := store.Devices(); len(devices) == 0 {
			fmt.Println("pair a browser: omaterm web pair")
		}
		if cfg.URL == "" && cfg.Tunnel == "" && !cfg.OnNetwork() {
			fmt.Println("reach it from other devices: omaterm web on --tunnel tailscale|cloudflare, or omaterm web on --network")
		}
		return nil
	case "off":
		if _, err := store.SetConfig(func(c *web.Config) { c.Enabled = false }); err != nil {
			return err
		}
		if _, err := daemon.ReloadWeb(false); err != nil && !errors.Is(err, client.ErrUnreachable) {
			return err
		}
		fmt.Println("web access off; paired devices stay paired (omaterm web revoke all)")
		return nil
	case "status":
		cfg, err := store.Config()
		if err != nil {
			return err
		}
		if !cfg.Enabled {
			fmt.Println("web access off: omaterm web on")
			return nil
		}
		st, err := daemon.ReloadWeb(false)
		switch {
		case errors.Is(err, client.ErrUnreachable):
			fmt.Println("web access on, served once the session daemon runs (open omaterm)")
		case err != nil:
			fmt.Printf("web access on, but not serving: %v\n", err)
		default:
			fmt.Printf("web access on, listening on %s\n", st.Addr)
		}
		sayTunnel(cfg.Tunnel, st)
		if cfg.OnNetwork() {
			fmt.Println(networkWarning)
		}
		fmt.Printf("devices reach it at %s\n", web.BaseURL(cfg, st.TunnelURL))
		devices, _ := store.Devices()
		fmt.Printf("%d paired %s (omaterm web devices)\n", len(devices), plural(len(devices), "device", "devices"))
		return nil
	case "pair":
		cfg, err := store.Config()
		if err != nil {
			return err
		}
		code, expires, err := store.NewCode()
		if err != nil {
			return err
		}
		// The tunnel the daemon runs, when one does, says where the link
		// lands; without a daemon the config alone tells.
		st, _ := daemon.StatusWeb()
		link := web.PairLink(web.BaseURL(cfg, st.TunnelURL), code)
		if rows, err := web.QR(link); err == nil && isTerminal(os.Stdout) {
			fmt.Print(web.QRText(rows))
		}
		fmt.Printf("open %s\n", link)
		fmt.Printf("or enter %s on the page; it works once, until %s\n", code, expires.Format("15:04"))
		if !cfg.Enabled {
			fmt.Println("web access is off: omaterm web on")
		}
		return nil
	case "devices":
		devices, err := store.Devices()
		if err != nil {
			return err
		}
		if len(devices) == 0 {
			fmt.Println("no paired devices: omaterm web pair")
			return nil
		}
		for _, d := range devices {
			fmt.Printf("%s  %-22s paired %s, last seen %s\n", d.ID, d.Name,
				time.UnixMilli(d.Created).Format("2006-01-02"), time.UnixMilli(d.LastSeen).Format("2006-01-02 15:04"))
		}
		return nil
	case "revoke":
		if len(args) != 1 {
			return usageErr("omaterm web revoke <device id|all>")
		}
		gone, err := store.Revoke(args[0])
		if err != nil {
			return err
		}
		if !gone && args[0] != "all" {
			return fmt.Errorf("%w: no device %s", errNotFound, args[0])
		}
		fmt.Println("signed out")
		return nil
	case "tunnel":
		cfg, _ := store.Config()
		avail := tunnel.Available()
		fmt.Printf(`omaterm carries web access, served on 127.0.0.1:%d, through a tunnel it runs itself:

  Tailscale, to your own devices on your tailnet:
    omaterm web on --tunnel tailscale
    then pair from https://<this machine>.<tailnet>.ts.net%s

  Cloudflare, a public https URL anyone can open:
    omaterm web on --tunnel cloudflare
    the page still asks a paired browser for its code

  Or run a tunnel yourself (tailscale serve, cloudflared), or keep web
  access to this machine:
    omaterm web on --url https://<the URL it prints>
    omaterm web on --local

`, cfg.ListenPort(), offTunnel(avail))
		return nil
	}
	return usageErr("omaterm web on [--network|--local|--host addr] [--port N] [--url U] [--tunnel tailscale|cloudflare|none]|off|status|pair|devices|revoke|tunnel")
}

// offTunnel says why a tunnel can't be run here, when one can't.
func offTunnel(avail map[tunnel.Provider]string) string {
	var why []string
	for _, p := range []tunnel.Provider{tunnel.Tailscale, tunnel.Cloudflare} {
		if r := avail[p]; r != "" {
			why = append(why, fmt.Sprintf("\n    (%s: %s)", p, r))
		}
	}
	return strings.Join(why, "")
}

// tunnelOf reads --tunnel's choice: tailscale, cloudflare, or none for
// no tunnel at all.
func tunnelOf(f *flags) (string, error) {
	v, ok := f.vals["tunnel"]
	if !ok {
		return "", nil
	}
	switch v {
	case "tailscale", "cloudflare":
		return v, nil
	case "none", "":
		return "", nil
	}
	return "", usageErr("--tunnel takes tailscale, cloudflare or none")
}

// sayTunnel reports the tunnel web access runs, or why it doesn't.
func sayTunnel(provider string, st web.Status) {
	if provider == "" {
		return
	}
	switch {
	case st.TunnelURL != "":
		fmt.Printf("tunnel (%s): %s\n", provider, st.TunnelURL)
	case st.TunnelError != "":
		fmt.Printf("tunnel (%s): %s\n", provider, st.TunnelError)
	default:
		fmt.Printf("tunnel (%s): still starting — omaterm web status\n", provider)
	}
}

// networkWarning goes with web access on the network, which is plain HTTP.
const networkWarning = "on your network, over plain HTTP: only paired browsers get in, but anyone on the network\n" +
	"can watch the traffic. Use it on networks you trust (omaterm web on --local to stop)"

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// isTerminal tells output a person reads, which a QR code is for.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

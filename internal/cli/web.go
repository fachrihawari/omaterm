package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
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
		f, err := parse(args, map[string]string{"port": "port", "p": "port", "url": "url"}, nil)
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
			if u, ok := f.vals["url"]; ok {
				c.URL = u
			}
		})
		if err != nil {
			return err
		}
		st, err := daemon.ReloadWeb(true)
		if err != nil {
			return err
		}
		fmt.Printf("web access on: http://%s\n", st.Addr)
		if devices, _ := store.Devices(); len(devices) == 0 {
			fmt.Println("pair a browser: omaterm web pair")
		}
		if cfg.URL == "" {
			fmt.Println("reach it from other devices: omaterm web tunnel")
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
			fmt.Printf("web access on: http://%s\n", st.Addr)
		}
		fmt.Printf("devices reach it at %s\n", web.BaseURL(cfg))
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
		link := web.PairLink(web.BaseURL(cfg), code)
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
		port := cfg.ListenPort()
		fmt.Printf(`omaterm serves web access on 127.0.0.1:%d only. Carry it with one of:

  Tailscale, to your own devices on your tailnet:
    tailscale serve --bg %d
  then pair from https://<this machine>.<tailnet>.ts.net

  Cloudflare, a public https URL (anyone can reach the page; only paired
  browsers get past it):
    cloudflared tunnel --url http://127.0.0.1:%d
  then: omaterm web on --url https://<the URL it prints>

`, port, port, port)
		return nil
	}
	return usageErr("omaterm web on|off|status|pair|devices|revoke|tunnel")
}

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

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
		f, err := parse(args, map[string]string{"port": "port", "p": "port", "url": "url", "host": "host", "tunnel": "tunnel",
			"hostname": "hostname", "token": "token"},
			map[string]string{"network": "network", "local": "local"})
		if err != nil {
			return err
		}
		via, err := tunnelOf(f)
		if err != nil {
			return err
		}
		_, viaSet := f.vals["tunnel"]
		cf, err := cloudflareOf(f, os.Stdin)
		if err != nil {
			return err
		}
		// A domain or token of one's own is Cloudflare's: it picks
		// Cloudflare, and goes with no other tunnel.
		ownDomain := cf.host != "" || cf.tokenSet
		if ownDomain && viaSet && via != string(tunnel.Cloudflare) {
			return usageErr("--hostname and --token are for --tunnel cloudflare")
		}
		if cur, _ := store.Config(); cf.tokenSet && !cf.hostSet && cur.CloudflareHost == "" {
			return usageErr("--token needs --hostname, the public hostname the tunnel has in Cloudflare")
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
			if viaSet {
				c.Tunnel = via
			} else if ownDomain {
				c.Tunnel = string(tunnel.Cloudflare)
			}
			if cf.hostSet {
				c.CloudflareHost = cf.host
				if cf.host == "" {
					c.CloudflareToken = "" // back to a quick tunnel
				}
			}
			if cf.tokenSet {
				c.CloudflareToken = cf.token
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
		sayLinkKept(cfg)
		if cfg.OnNetwork() {
			fmt.Println(networkWarning)
		}
		if devices, _ := store.Devices(); len(devices) == 0 {
			fmt.Println("pair a browser: omaterm web pair")
		}
		if cfg.URL == "" && cfg.Tunnel == "" && !cfg.OnNetwork() {
			fmt.Println("reach it from other devices: omaterm web on --tunnel tailscale|funnel|cloudflare, or omaterm web on --network")
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
		sayLinkKept(cfg)
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
			notified := ""
			if d.Push != nil {
				notified = ", notified"
			}
			fmt.Printf("%s  %-22s paired %s, last seen %s%s\n", d.ID, d.Name,
				time.UnixMilli(d.Created).Format("2006-01-02"), time.UnixMilli(d.LastSeen).Format("2006-01-02 15:04"), notified)
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

  Tailscale, to your own devices on your tailnet; its link stays the same:
    omaterm web on --tunnel tailscale
    then pair from https://<this machine>.<tailnet>.ts.net%s

  Tailscale Funnel, a public link that stays the same, with no app on the phone:
    omaterm web on --tunnel funnel
    turn Funnel on for your tailnet once: the first try gives the link for it

  Cloudflare with your own domain, a public link that stays the same. In the
  Cloudflare dashboard: Zero Trust › Networks › Tunnels › Create a tunnel
  (cloudflared). Add a public hostname, as term.example.com, whose service is
  http://localhost:%d, and copy the tunnel's token. Then:
    omaterm web on --tunnel cloudflare --hostname term.example.com --token -
    and paste the token: - reads it from stdin, out of your shell's history

  Cloudflare quick tunnel, a public link that changes each time it starts:
    omaterm web on --tunnel cloudflare
    omaterm web on --hostname "" goes back to it from your own domain

  Only a paired browser gets in, by any link. A link that changes means
  pairing the phone again; one that stays the same, pairing once.

  Or run a tunnel yourself (tailscale serve, cloudflared), or keep web
  access to this machine:
    omaterm web on --url https://<the URL it prints>
    omaterm web on --local

`, cfg.ListenPort(), offTunnel(avail), cfg.ListenPort())
		return nil
	}
	return usageErr("omaterm web on [--network|--local|--host addr] [--port N] [--url U] [--tunnel tailscale|funnel|cloudflare|none] [--hostname H --token T|-]|off|status|pair|devices|revoke|tunnel")
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

// tunnelOf reads --tunnel's choice: tailscale, funnel, cloudflare, or
// none for no tunnel at all.
func tunnelOf(f *flags) (string, error) {
	v, ok := f.vals["tunnel"]
	if !ok {
		return "", nil
	}
	switch v {
	case "tailscale", "funnel", "cloudflare":
		return v, nil
	case "none", "":
		return "", nil
	}
	return "", usageErr("--tunnel takes tailscale, funnel, cloudflare or none")
}

// cloudflareFlags is what --hostname and --token ask of the user's own
// Cloudflare tunnel; set tells a flag given.
type cloudflareFlags struct {
	host, token       string
	hostSet, tokenSet bool
}

// cloudflareOf reads --hostname and --token: the hostname bare or from its
// URL, "" to go back to a quick tunnel; the token as given, or - to read
// it from stdin (one line), which keeps it out of the shell's history.
func cloudflareOf(f *flags, stdin io.Reader) (cloudflareFlags, error) {
	var cf cloudflareFlags
	if v, ok := f.vals["hostname"]; ok {
		h, err := tunnel.ParseHostname(v)
		if err != nil {
			return cf, usageErr("--hostname takes the tunnel's public hostname, as term.example.com, with no path or port")
		}
		cf.host, cf.hostSet = h, true
	}
	if v, ok := f.vals["token"]; ok {
		if cf.hostSet && cf.host == "" {
			return cf, usageErr(`--hostname "" goes back to a quick tunnel, forgetting the token: leave --token out`)
		}
		if v == "-" {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && err != io.EOF {
				return cf, fmt.Errorf("reading the token from stdin: %w", err)
			}
			v = line
		}
		if v = strings.TrimSpace(v); v == "" {
			return cf, usageErr("--token takes the tunnel's token, or - to read it from stdin")
		}
		cf.token, cf.tokenSet = v, true
	}
	return cf, nil
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

// sayLinkKept tells a Cloudflare tunnel whose link changes each time it
// starts how to keep one: a link that changes means pairing again.
func sayLinkKept(cfg web.Config) {
	if cfg.Tunnel != string(tunnel.Cloudflare) || cfg.URL != "" || cfg.CloudflareToken != "" {
		return // kept, or the tunnel's error says what it lacks
	}
	if cfg.CloudflareHost != "" {
		fmt.Printf("a quick tunnel runs until %s has its token: omaterm web on --token -\n", cfg.CloudflareHost)
		return
	}
	fmt.Println("a quick tunnel's link changes each time it starts: omaterm web tunnel says how to keep one")
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

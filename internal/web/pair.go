package web

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"omaterm/internal/tunnel"
)

// BaseURL is where devices reach web access: the configured URL, else
// the live tunnel's, else this machine's Tailscale name when tailscale
// serve forwards to the port, else its address on the network when it
// listens there, else the loopback address, which only this machine can
// open. The tunnel URL is what the daemon's status says, so a link lands
// where the tunnel really serves.
func BaseURL(cfg Config, tunnelURL string) string {
	if cfg.URL != "" {
		return strings.TrimRight(cfg.URL, "/")
	}
	if tunnelURL != "" {
		return strings.TrimRight(tunnelURL, "/")
	}
	if u := tailscaleURL(cfg.ListenPort()); u != "" {
		return u
	}
	if cfg.OnNetwork() {
		if ip := networkIP(cfg.Host); ip != "" {
			return "http://" + net.JoinHostPort(ip, strconv.Itoa(cfg.ListenPort()))
		}
	}
	return "http://127.0.0.1:" + strconv.Itoa(cfg.ListenPort())
}

// networkIP is the address other devices reach this machine at: host
// itself when it names one, else the one the default route leaves from.
func networkIP(host string) string {
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		return ip.String()
	}
	// Connecting UDP sends nothing; it only picks the route.
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// tailscaleURL is https://<this machine's tailnet name> when tailscale
// serve forwards to port.
func tailscaleURL(port int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tunnel.TailscaleBin(), "serve", "status", "--json")
	hide(cmd)
	serve, err := cmd.Output()
	if err != nil || !strings.Contains(string(serve), ":"+strconv.Itoa(port)) {
		return ""
	}
	cmd = exec.CommandContext(ctx, tunnel.TailscaleBin(), "status", "--json")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var st struct{ Self struct{ DNSName string } }
	if json.Unmarshal(out, &st) != nil || st.Self.DNSName == "" {
		return ""
	}
	return "https://" + strings.TrimSuffix(st.Self.DNSName, ".")
}

// PairLink is a link that pairs the browser opening it: the code rides
// after the #, which no server logs.
func PairLink(base, code string) string { return base + "/#code=" + code }

// QR is a link's QR code, as rows of modules (true is dark), with the
// quiet zone around it.
func QR(text string) ([][]bool, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	const quiet = 2
	n := c.Size + 2*quiet
	rows := make([][]bool, n)
	for y := range rows {
		rows[y] = make([]bool, n)
		for x := range rows[y] {
			rows[y][x] = c.Black(x-quiet, y-quiet)
		}
	}
	return rows, nil
}

// QRText draws a QR code for a terminal, two modules a character, dark on
// a white background whatever the terminal's colors.
func QRText(rows [][]bool) string {
	var b strings.Builder
	for y := 0; y < len(rows); y += 2 {
		b.WriteString("\x1b[30;47m")
		for x := range rows[y] {
			top := rows[y][x]
			bottom := y+1 < len(rows) && rows[y+1][x]
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

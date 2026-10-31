package web

import (
	"strings"
	"testing"
)

func TestBaseURL(t *testing.T) {
	if got := BaseURL(Config{URL: "https://abc.trycloudflare.com/"}, ""); got != "https://abc.trycloudflare.com" {
		t.Fatalf("configured URL: %q", got)
	}
	if got := BaseURL(Config{URL: "https://mine.example.com/"}, "https://live.trycloudflare.com"); got != "https://mine.example.com" {
		t.Fatalf("the configured URL wins: %q", got)
	}
	if got := BaseURL(Config{Port: 7400}, "https://box.tailnet.ts.net/"); got != "https://box.tailnet.ts.net" {
		t.Fatalf("the live tunnel's URL: %q", got)
	}
	if got := BaseURL(Config{Host: "192.168.1.20", Port: 7400}, ""); got != "http://192.168.1.20:7400" {
		t.Fatalf("on the network: %q", got)
	}
	if got := BaseURL(Config{Port: 7400}, ""); got != "http://127.0.0.1:7400" {
		t.Fatalf("on the loopback: %q", got)
	}
	if got := PairLink("https://x.ts.net", "ABCD-EFGH"); got != "https://x.ts.net/#code=ABCD-EFGH" {
		t.Fatalf("link: %q", got)
	}
}

// The QR code is square, with its quiet zone, and its terminal drawing
// takes two rows a line.
func TestQR(t *testing.T) {
	rows, err := QR("https://box.tailnet.ts.net/#code=ABCD-EFGH")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if len(r) != len(rows) {
			t.Fatal("not square")
		}
	}
	if rows[0][0] || rows[len(rows)-1][len(rows)-1] {
		t.Fatal("no quiet zone")
	}
	lines := strings.Count(QRText(rows), "\n")
	if lines != (len(rows)+1)/2 {
		t.Fatalf("%d lines for %d rows", lines, len(rows))
	}
}

func TestListenAddr(t *testing.T) {
	for _, c := range []struct {
		cfg     Config
		addr    string
		network bool
	}{
		{Config{}, "127.0.0.1:7370", false},
		{Config{Host: "localhost", Port: 8000}, "localhost:8000", false},
		{Config{Host: "0.0.0.0"}, "0.0.0.0:7370", true},
		{Config{Host: "::"}, "[::]:7370", true},
		{Config{Host: "192.168.1.20"}, "192.168.1.20:7370", true},
	} {
		if got := c.cfg.ListenAddr(); got != c.addr || c.cfg.OnNetwork() != c.network {
			t.Errorf("%+v: %s, on the network %v", c.cfg, got, c.cfg.OnNetwork())
		}
	}
}

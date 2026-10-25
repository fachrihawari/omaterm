package web

import (
	"strings"
	"testing"
)

func TestBaseURL(t *testing.T) {
	if got := BaseURL(Config{URL: "https://abc.trycloudflare.com/"}); got != "https://abc.trycloudflare.com" {
		t.Fatalf("configured URL: %q", got)
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

package daemon

import (
	"strings"
	"testing"
)

func TestUnitQuotesExecStart(t *testing.T) {
	u := Unit(`/home/a b/100%/"x"/omaterm`)
	want := `ExecStart="/home/a b/100%%/\"x\"/omaterm" daemon serve`
	if !strings.Contains(u, want+"\n") {
		t.Fatalf("unit:\n%s\nwant line %s", u, want)
	}
	for _, line := range []string{"Environment=OMATERM_SERVICE=1", "Restart=always", "WantedBy=default.target"} {
		if !strings.Contains(u, line) {
			t.Fatalf("unit lacks %s", line)
		}
	}
}

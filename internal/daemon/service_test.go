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

// A build is its version and its executable's hash: two builds of one
// version differ, which the app tells to offer restarting sessions.
func TestBuild(t *testing.T) {
	b := Build("0.7.0")
	if !strings.HasPrefix(b, "0.7.0+") || len(b) != len("0.7.0+")+12 || VersionOf(b) != "0.7.0" {
		t.Fatalf("build %q", b)
	}
	if Build("0.7.0") != b {
		t.Fatal("not stable")
	}
}

package tunnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseServe(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		want     map[int][]string
	}{
		{"nothing", `{}`, map[int][]string{}},
		{"empty output", ``, map[int][]string{}},
		{"web and tcp", `{"TCP":{"443":{"HTTPS":true},"8443":{"HTTPS":true},"2222":{"TCPForward":"127.0.0.1:22"}},
			"Web":{"box.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7370"}}},
			"box.tailnet.ts.net:8443":{"Handlers":{"/":{"Text":"hi"}}}}}`,
			map[int][]string{443: {"http://127.0.0.1:7370"}, 8443: nil, 2222: nil}},
		{"a foreground serve", `{"Foreground":{"abc":{"TCP":{"10000":{"HTTPS":true}},
			"Web":{"box.tailnet.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://localhost:3000"}}}}}}}`,
			map[int][]string{10000: {"http://localhost:3000"}}},
	} {
		got, err := parseServe([]byte(tt.in))
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %v %v, want %v", tt.name, got, err, tt.want)
		}
	}
	if _, err := parseServe([]byte(`not json`)); err == nil {
		t.Error("garbage was parsed")
	}
}

const (
	userServes443  = `{"TCP":{"443":{"HTTPS":true}},"Web":{"box.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`
	ourServes443   = `{"TCP":{"443":{"HTTPS":true}},"Web":{"box.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7370"}}}}}`
	fileOn8443Only = `{"TCP":{"8443":{"HTTPS":true}},"Web":{"box.tailnet.ts.net:8443":{"Handlers":{"/":{"Text":"hello"}}}}}`
)

func TestTailscaleRefusesAPortTheUserServes(t *testing.T) {
	for _, tt := range []struct {
		name, serves string
		port         int
		fail         bool // serve status can't be read
		refused      bool
	}{
		{"another address on 443", userServes443, 0, false, true},
		{"our own address already there", ourServes443, 0, false, false},
		{"a file served there", fileOn8443Only, 8443, false, true},
		{"a free port", userServes443, 8443, false, false},
		{"nothing served", `{}`, 0, false, false},
		{"unreadable: assume free", userServes443, 0, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := tunnelTest(t)
			fakePATH(t, map[string]string{"tailscale": fakeTailscale})
			t.Setenv("TS_SERVE", tt.serves)
			if tt.fail {
				t.Setenv("TS_SERVE_FAIL", "1")
			}
			tun, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370", Name: "web access", Port: tt.port})
			var inUse *PortInUseError
			if tt.refused {
				want := tt.port
				if want == 0 {
					want = 443
				}
				if !errors.As(err, &inUse) || inUse.Port != want || !strings.Contains(err.Error(), "already serves another address on") {
					t.Fatalf("error %v", err)
				}
				if b, _ := os.ReadFile(filepath.Join(dir, "tailscale.log")); len(b) != 0 {
					t.Fatalf("tailscale was told to serve: %q", b)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tun.Stop()
		})
	}
}

func TestServedPortsAreKeptAndForgotten(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	file := filepath.Join(t.TempDir(), "tailscale-serves.json")
	TrackServes(file)
	defer TrackServes("")
	a, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:7370", Name: "web access"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Start(context.Background(), Spec{Provider: Tailscale, Target: "http://127.0.0.1:3001", Name: "shop", Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if got := readServeLog(file); !reflect.DeepEqual(got, []int{443, 8443}) {
		t.Fatalf("kept %v", got)
	}
	a.Stop()
	if got := readServeLog(file); !reflect.DeepEqual(got, []int{8443}) {
		t.Fatalf("after one stops: %v", got)
	}
	b.Stop()
	if got := readServeLog(file); len(got) != 0 {
		t.Fatalf("after both: %v", got)
	}
}

func TestCleanupTurnsOffWhatACrashLeft(t *testing.T) {
	dir := tunnelTest(t)
	fakePATH(t, map[string]string{"tailscale": fakeTailscale})
	file := filepath.Join(t.TempDir(), "tailscale-serves.json")
	TrackServes(file)
	defer TrackServes("")
	// Listed: 443 and 8443 still serve omaterm's addresses, 10000 the user's own,
	// and 10001 nothing at all.
	if err := writeServeLog(file, []int{443, 8443, 10000, 10001}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TS_SERVE", `{"TCP":{"443":{},"8443":{},"10000":{},"9999":{}},"Web":{
		"box.tailnet.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7370"}}},
		"box.tailnet.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:41234"}}},
		"box.tailnet.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000/app"}}},
		"box.tailnet.ts.net:9999":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:5000"}}}}}`)
	off, err := CleanupServes()
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(off)
	if !reflect.DeepEqual(off, []int{443, 8443}) {
		t.Fatalf("turned off %v", off)
	}
	served, _ := os.ReadFile(filepath.Join(dir, "tailscale.log"))
	if got := string(served); got != "serve --https=443 off\nserve --https=8443 off\n" {
		t.Fatalf("tailscale ran %q", got)
	}
	if got := readServeLog(file); len(got) != 0 {
		t.Fatalf("the list wasn't emptied: %v", got)
	}
	// Nothing listed: tailscale isn't asked anything.
	os.Remove(filepath.Join(dir, "tailscale.log"))
	if off, err := CleanupServes(); err != nil || off != nil {
		t.Fatalf("%v %v", off, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tailscale.log")); len(b) != 0 {
		t.Fatalf("tailscale ran for an empty list: %q", b)
	}
}

func TestCleanupWithTailscaleGoneJustEmptiesTheList(t *testing.T) {
	tunnelTest(t)
	fakePATH(t, nil) // no tailscale
	file := filepath.Join(t.TempDir(), "tailscale-serves.json")
	TrackServes(file)
	defer TrackServes("")
	writeServeLog(file, []int{443})
	if _, err := CleanupServes(); err == nil {
		t.Log("an error is allowed here: tailscale is missing")
	}
	if got := readServeLog(file); len(got) != 0 {
		t.Fatalf("the list stayed: %v", got)
	}
}

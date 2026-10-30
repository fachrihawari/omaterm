package cli

import (
	"testing"

	"omaterm/internal/proto"
)

func TestPortNumber(t *testing.T) {
	for _, c := range []struct {
		want string
		n    int
		ok   bool
	}{
		{"5173", 5173, true},
		{"80", 80, true},
		{"vite", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"70000", 0, false},
	} {
		if n, ok := portNumber(c.want); n != c.n || ok != c.ok {
			t.Errorf("%q: got %d %v", c.want, n, ok)
		}
	}
}

func TestMatchPort(t *testing.T) {
	ports := []proto.Port{
		{Port: 5173, Name: "vite", Process: "node", Open: true},
		{Port: 8080, Name: "bun", Process: "bun", Open: true},
	}
	cases := []struct {
		want  string
		port  int
		found bool
	}{
		{"vite", 5173, true},
		{"VITE", 5173, true},
		{"node", 5173, true}, // the process under the name it shows
		{"8080", 8080, true},
		{"5173", 5173, true},
		{"3000", 3000, true}, // a port known before anything binds it
		{"postgres", 0, false},
	}
	for _, c := range cases {
		n, numbered := portNumber(c.want)
		p, ok := matchPort(ports, c.want, n, numbered)
		if ok != c.found || p.Port != c.port {
			t.Errorf("%q: got %d %v, want %d %v", c.want, p.Port, ok, c.port, c.found)
		}
	}
}

func TestMatchPortNamesAPortItKnows(t *testing.T) {
	n, numbered := portNumber("3000")
	p, ok := matchPort(nil, "3000", n, numbered)
	if !ok || p.URL != "http://localhost:3000" || p.Name != "" {
		t.Fatalf("got %+v %v", p, ok)
	}
}

func TestFit(t *testing.T) {
	if got := fit("web", 14); got != "web" {
		t.Errorf("short: %q", got)
	}
	if got := fit("a-very-long-process-name", 14); len([]rune(got)) != 14 || got[len(got)-3:] != "…" {
		t.Errorf("long: %q", got)
	}
}

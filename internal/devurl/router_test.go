package devurl

import (
	"reflect"
	"testing"
)

func TestRouterFirstOfAHostRoutes(t *testing.T) {
	r := NewRouter()
	refused := r.Set([]Route{
		{Host: "shop.localhost", Project: "x", Process: "web", Port: 1},
		{Host: "api.localhost", Project: "x", Process: "api", Port: 2},
		{Host: "Shop.localhost", Project: "y", Process: "web", Port: 3},
	})
	if len(refused) != 1 || refused[0].Project != "y" {
		t.Fatalf("refused %v", refused)
	}
	rt, ok := r.Lookup("shop.localhost:8443")
	if !ok || rt.Process != "web" || rt.Project != "x" {
		t.Fatalf("lookup: %v %v", rt, ok)
	}
	if _, ok := r.Lookup("nope.localhost"); ok {
		t.Fatal("an unknown host routed")
	}
	if got := len(r.Routes()); got != 2 {
		t.Fatalf("routes %d", got)
	}
}

func TestRouterSetReplacesAll(t *testing.T) {
	r := NewRouter()
	r.Set([]Route{{Host: "a.localhost"}})
	r.Set([]Route{{Host: "b.localhost"}})
	if _, ok := r.Lookup("a.localhost"); ok {
		t.Fatal("the old route stayed")
	}
	if got := r.Routes(); !reflect.DeepEqual(got, []Route{{Host: "b.localhost"}}) {
		t.Fatalf("routes %v", got)
	}
}

func TestHostOnly(t *testing.T) {
	for in, want := range map[string]string{"A.localhost": "a.localhost", "a.localhost:443": "a.localhost", "a.localhost.": "a.localhost", "[::1]:80": "::1"} {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

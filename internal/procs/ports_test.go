package procs

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func envOf(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// startForPorts runs def in a supervisor with a fake runner and returns the
// options it was started with and the process as listed.
func startForPorts(t *testing.T, def Def) (*fakeRunner, Proc) {
	t.Helper()
	oldAlive := aliveDelay
	aliveDelay = 20 * time.Millisecond
	t.Cleanup(func() { aliveDelay = oldAlive })
	dir := t.TempDir()
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	t.Cleanup(s.Close)
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", def.Name); err != nil {
		t.Fatal(err)
	}
	return r, waitProc(t, s, "p", def.Name, Running)
}

func TestProcessWithAURLGetsAFreePortAtEachStart(t *testing.T) {
	oldAlive := aliveDelay
	aliveDelay = 20 * time.Millisecond
	defer func() { aliveDelay = oldAlive }()
	dir := t.TempDir()
	def := Def{Name: "web", Command: "serve", URL: "shop"}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	defer s.Close()
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	proc := waitProc(t, s, "p", "web", Running)
	_, o := r.latest()
	got, ok := envOf(o.Env, "PORT")
	n, err := strconv.Atoi(got)
	if !ok || err != nil || n < 1024 {
		t.Fatalf("PORT %q in %v", got, o.Env)
	}
	if proc.Port != n {
		t.Fatalf("Proc.Port %d, PORT %d", proc.Port, n)
	}
	// The port is free: nothing listens on it.
	ln, err := net.Listen("tcp", "127.0.0.1:"+got)
	if err != nil {
		t.Fatalf("the port isn't free: %v", err)
	}
	ln.Close()
	// A definition reloaded under the run keeps the port it shows.
	s.Load("p", dir, []Def{def})
	if p := s.List("p")[0]; p.Port != n {
		t.Fatalf("Load lost the port: %d", p.Port)
	}
	// A stop gives it back, and the next start asks for another.
	if err := s.Stop("p", "web"); err != nil {
		t.Fatal(err)
	}
	if p := s.List("p")[0]; p.Port != 0 {
		t.Fatalf("a stopped process shows port %d", p.Port)
	}
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "web", Running)
	_, o = r.latest()
	if second, _ := envOf(o.Env, "PORT"); second == "" {
		t.Fatalf("no PORT at the second start: %v", o.Env)
	}
}

func TestReadinessUsesThePickedPort(t *testing.T) {
	oldAlive := aliveDelay
	aliveDelay = time.Minute // only a listening port can make it running in time
	defer func() { aliveDelay = oldAlive }()
	dir := t.TempDir()
	def := Def{Name: "web", Command: "serve", URL: "shop"}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	defer s.Close()
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	_, o := r.latest()
	port, _ := envOf(o.Env, "PORT")
	time.Sleep(3 * checkPeriod)
	if p := s.List("p")[0]; p.Status != Starting {
		t.Fatalf("running with nothing listening: %v", p.Status)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	waitProc(t, s, "p", "web", Running)
}

func TestPortEnvForADefinedPort(t *testing.T) {
	tests := []struct {
		name     string
		def      Def
		wantPort string // "" for no PORT in the env
		wantProc int
	}{
		{"a port is PORT", Def{Name: "web", Command: "c", Port: 4321}, "4321", 4321},
		{"a port with a url keeps its port", Def{Name: "web", Command: "c", Port: 4321, URL: "shop"}, "4321", 4321},
		{"the env's PORT is left alone", Def{Name: "web", Command: "c", Port: 4321, Env: map[string]string{"PORT": "9"}}, "9", 4321},
		{"the env's PORT is the url's port", Def{Name: "web", Command: "c", URL: "shop", Env: map[string]string{"PORT": "4322"}}, "4322", 4322},
		{"no port and no url sets nothing", Def{Name: "web", Command: "c"}, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, proc := startForPorts(t, tt.def)
			_, o := r.latest()
			got, ok := envOf(o.Env, "PORT")
			if tt.wantPort == "" && ok || tt.wantPort != "" && got != tt.wantPort {
				t.Errorf("PORT %q (set %v), want %q in %v", got, ok, tt.wantPort, o.Env)
			}
			if tt.wantPort != "" {
				if n := strings.Count(strings.Join(o.Env, " "), "PORT="); n != 1 {
					t.Errorf("PORT set %d times in %v", n, o.Env)
				}
			}
			if proc.Port != tt.wantProc {
				t.Errorf("Proc.Port %d, want %d", proc.Port, tt.wantProc)
			}
		})
	}
}

func TestAllListsEveryProjectInOrder(t *testing.T) {
	s := New(newFakeRunner(), nil)
	defer s.Close()
	s.Load("b", "/b", []Def{{Name: "y", Command: "c"}, {Name: "x", Command: "c"}})
	s.Load("a", "/a", []Def{{Name: "z", Command: "c"}})
	var got []string
	for _, r := range s.All() {
		got = append(got, r.Project+"/"+r.Name)
	}
	if strings.Join(got, " ") != "a/z b/y b/x" {
		t.Fatalf("%v", got)
	}
}

func TestDecorateAppliesToListOnly(t *testing.T) {
	s := New(newFakeRunner(), nil)
	defer s.Close()
	s.Decorate = func(project string, p *Proc) { p.Link = "https://" + project + ".localhost" }
	s.Load("a", "/a", []Def{{Name: "z", Command: "c"}})
	if got := s.List("a")[0].Link; got != "https://a.localhost" {
		t.Fatalf("link %q", got)
	}
	if got := s.All()[0].Link; got != "" {
		t.Fatalf("All carries the link %q", got)
	}
}

// A server on ::1 only, as Vite on macOS, is up as much as one on
// 127.0.0.1.
func TestListeningOnEitherLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Logf("skipping %s: %v", addr, err)
			continue
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if !listening(port) {
			t.Errorf("%s: not listening", ln.Addr())
		}
		ln.Close()
		if listening(port) {
			t.Errorf("%s: listening once closed", ln.Addr())
		}
	}
}

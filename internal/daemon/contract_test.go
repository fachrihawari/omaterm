//go:build linux || darwin

package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"omaterm/internal/session"
)

// The app drives panes through session.Host the same whether they run in
// it or in the daemon: one suite, both hosts.
func TestHostContract(t *testing.T) {
	hosts := map[string]func(t *testing.T) session.Host{
		"local": func(t *testing.T) session.Host {
			h := session.NewLocalHost()
			ctx, cancel := context.WithCancel(context.Background())
			go h.Run(ctx)
			t.Cleanup(func() { cancel(); h.CloseAll() })
			return h
		},
		"daemon": func(t *testing.T) session.Host {
			_, socket := startDaemon(t, time.Minute)
			r, err := Dial(socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(r.Close)
			return r
		},
	}
	for name, mk := range hosts {
		t.Run(name, func(t *testing.T) {
			h := mk(t)
			exited := make(chan int, 1)
			h.SetHooks(session.Hooks{OnExit: func(id string, code int) { exited <- code }})
			p, err := h.Start(session.StartOptions{ID: "c1", Session: "s1", Exec: true,
				Command: []string{"sh", "-c", "read line; echo got-$line; exit 4"}, Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			if h.Get("c1") == nil || h.Len() != 1 || h.Infos()["c1"].Session != "s1" {
				t.Fatalf("pane not listed: %+v", h.Infos())
			}
			if p.Term() == nil {
				t.Fatal("no terminal")
			}
			// What the user types, then what the omaterm command types:
			// the latter waits for the former to end.
			if err := p.Write([]byte("ab")); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.Send(ctx, "c\r", false); err != nil {
				t.Fatal(err)
			}
			cancel()
			if time.Since(start) < 1500*time.Millisecond {
				t.Fatal("sent while the user typed")
			}
			ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, err := session.Await(ctx, p, nil, true)
			if err != nil || info.Status != session.Exited || info.ExitCode == nil || *info.ExitCode != 4 {
				t.Fatalf("await: %+v %v", info, err)
			}
			if txt := p.Text(true, 0); !strings.Contains(txt, "got-abc") {
				t.Fatalf("text %q lacks got-abc", txt)
			}
			select {
			case code := <-exited:
				if code != 4 {
					t.Fatalf("exit hook code %d", code)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no exit hook")
			}
			h.Remove("c1")
			waitFor(t, "removed", func() bool { return h.Get("c1") == nil && h.Len() == 0 })
		})
	}
}

// What the app's terminal shows of a daemon pane is what the pane's own
// terminal holds: text, colors' cells and the cursor, through scrolling,
// erasing and the alternate screen.
func TestMirrorFidelity(t *testing.T) {
	_, socket := startDaemon(t, time.Minute)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	script := `for i in $(seq 1 40); do printf '\033[3%dmline %d\033[0m\n' $((i % 8)) $i; done
printf 'abc\033[2Dxy\033[K\n'
printf '\033[?1049hALT\033[?1049l'
printf 'tail \033[1mbold\033[0m'
printf '\033[5;10H'
exec cat`
	h, err := r.Start(session.StartOptions{ID: "m1", Exec: true, Command: []string{"sh", "-c", script}, Cols: 60, Rows: 20})
	if err != nil {
		t.Fatal(err)
	}
	view := h.Term()
	if view == nil {
		t.Fatal("no terminal")
	}
	src := func() string { return h.Text(false, 0) }
	waitFor(t, "screen", func() bool { return strings.Contains(src(), "tail bold") })
	waitFor(t, "mirror", func() bool { return strings.TrimRight(view.Text(), "\n ") != "" && sameScreen(view.Text(), src()) })
	for _, want := range []string{"line 40", "axy", "tail bold"} {
		if !strings.Contains(view.Text(), want) {
			t.Fatalf("mirror lacks %q:\n%s", want, view.Text())
		}
	}
	if strings.Contains(src(), "ALT") || strings.Contains(view.Text(), "ALT") {
		t.Fatal("alternate screen leaked")
	}
}

// sameScreen compares the visible screens, the last rows of each text.
func sameScreen(view, src string) bool {
	tail := func(s string) []string {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		if len(lines) > 18 {
			lines = lines[len(lines)-18:]
		}
		return lines
	}
	a, b := tail(view), tail(src)
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}

// A client too far behind is dropped, so it never holds the pane back: it
// attaches again from a first frame.
func TestSlowClientDropped(t *testing.T) {
	at := &attachment{out: make(chan []byte, 2), done: make(chan struct{})}
	for i := 0; i < 3; i++ {
		at.send('o', []byte("x"))
	}
	select {
	case <-at.done:
	default:
		t.Fatal("a full queue did not drop the client")
	}
}

// A successor waits for the daemon it replaces to let go of the lock.
func TestTakeoverWaitsForLock(t *testing.T) {
	d, socket := startDaemon(t, time.Minute)
	t.Setenv("OMATERM_TAKEOVER", "1")
	go func() {
		time.Sleep(300 * time.Millisecond)
		d.Stop()
	}()
	next, err := Start(Options{Socket: socket, StateDir: t.TempDir(), IdleExit: time.Minute, Version: "next"})
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	next.Stop()
}

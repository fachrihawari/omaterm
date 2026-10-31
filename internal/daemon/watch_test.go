package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"omaterm/service"
)

// TestWatchFilesRunsInTheDaemon proves the folder watcher works in the
// daemon process, which runs no app: a supervisor that restarts a process
// on a change needs the watch, and the daemon has no window to run it in.
//
// mygo's WatchFiles panics outside an app on the main thread, so the test
// makes its watch on a goroutine of its own, as the daemon would.
func TestWatchFilesRunsInTheDaemon(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type made struct {
		w   *mygo.FileWatcher
		err error
	}
	ready := make(chan made, 1)
	go func() {
		w, err := mygo.WatchFiles(ctx, dir, mygo.FileWatchOptions{
			Recursive: true,
			Debounce:  50 * time.Millisecond,
			Ignore:    service.IgnoreFunc(dir),
		})
		ready <- made{w, err}
	}()
	got := <-ready
	if got.err != nil {
		t.Fatalf("WatchFiles in the daemon: %v", got.err)
	}
	w := got.w
	defer w.Close()

	// The watch takes its first look when it starts: a file already there
	// is not a change, so the writes come after it has.
	time.Sleep(300 * time.Millisecond)

	write := func(name string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt")
	write("node_modules/x.js")

	// Read what the watch reports for a second, without blocking on Next:
	// it waits for a change, and a change may not come again.
	type result struct {
		events []mygo.FileEvent
		err    error
	}
	events := make(chan result, 16)
	go func() {
		for {
			ev, err := w.Next(ctx)
			select {
			case events <- result{ev, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case r := <-events:
			if r.err != nil {
				t.Fatalf("Next: %v", r.err)
			}
			for _, e := range r.events {
				seen[e.Path] = true
				if e.Path == "node_modules/x.js" || e.Path == "node_modules" {
					t.Fatalf("the watch reported %q, which git ignores", e.Path)
				}
			}
		case <-deadline:
			if !seen["a.txt"] {
				t.Fatalf("the watch did not report a.txt: %v", seen)
			}
			return
		}
	}
}

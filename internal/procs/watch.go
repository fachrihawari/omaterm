package procs

import (
	"context"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// WatchFunc watches root for changes until ctx ends, calling changed with
// the paths (relative to root, forward slashes) of each batch of changes.
// The supervisor's owner sets it: the daemon with mygo.WatchFiles.
type WatchFunc func(ctx context.Context, root string, changed func(paths []string)) error

// watchDebounce is how long a change waits for the ones after it, so that
// an editor writing a file in three steps restarts the process once.
var watchDebounce = 300 * time.Millisecond

// pollPeriod is how often the files a watch's globs match are looked at
// when there is no folder watch to ask: on a system where mygo's watch
// cannot run, the process still restarts, a second later.
var pollPeriod = time.Second

// pollLimit bounds a poll's walk of the project: a folder larger than this
// is not walked every second, and its later files go unwatched.
const pollLimit = 5000

// watching reports whether the proc has globs to watch, and if so starts
// the goroutine that restarts it: one goroutine per run, ended by ctx.
func (s *Supervisor) watching(project, name string, def Def, root string, gen uint64) {
	if len(def.Watch) == 0 || s.Watch == nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	_, p := s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed {
		s.mu.Unlock()
		cancel()
		return
	}
	p.watch = cancel
	// Added under the same lock Close sets closed in, so a watch is
	// either counted by its Wait or never started.
	s.watches.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.watches.Done()
		s.watchChanges(ctx, project, name, def, root, gen)
	}()
}

// watchChanges restarts the proc when a file its globs name changes, once
// per burst, until ctx ends.
func (s *Supervisor) watchChanges(ctx context.Context, project, name string, def Def, root string, gen uint64) {
	debounce := watchDebounce
	if debounce <= 0 {
		debounce = 300 * time.Millisecond
	}
	// A change starts a timer; the ones that come before it fires join it.
	var (
		mu      sync.Mutex
		pending []string
		timer   *time.Timer
	)
	changed := func(paths []string) {
		var hit []string
		for _, path := range paths {
			if s.matches(def.Watch, path) {
				hit = append(hit, path)
			}
		}
		if len(hit) == 0 {
			return
		}
		mu.Lock()
		pending = append(pending, hit...)
		if timer == nil {
			timer = time.AfterFunc(debounce, func() {
				mu.Lock()
				batch := pending
				pending, timer = nil, nil
				mu.Unlock()
				if len(batch) == 0 || ctx.Err() != nil {
					return
				}
				s.watchRestart(project, name, gen, batch[0])
			})
		}
		mu.Unlock()
	}

	done := make(chan error, 1)
	go func() { done <- s.Watch(ctx, root, changed) }()
	select {
	case <-ctx.Done():
		return
	case err := <-done:
		if err == nil || ctx.Err() != nil {
			return
		}
		// No watch to ask: the files are looked at instead, so the
		// process still restarts, and says why it does it the slower way.
		s.setWatchMessage(project, name, gen, "watch failed: "+err.Error())
	}
	s.pollChanges(ctx, project, name, def, root, gen, changed)
}

// pollChanges looks at the files the globs match, and reports the ones
// whose stamp changed, until ctx ends.
func (s *Supervisor) pollChanges(ctx context.Context, project, name string, def Def, root string, gen uint64, changed func([]string)) {
	period := pollPeriod
	if period <= 0 {
		period = time.Second
	}
	seen := map[string]stamp{}
	first := true
	tick := time.NewTicker(period)
	defer tick.Stop()
	for {
		now := map[string]stamp{}
		for _, path := range s.pollFiles(def.Watch, root) {
			now[path] = stampOf(filepath.Join(root, path))
		}
		if !first {
			var hits []string
			for path, st := range now {
				if old, ok := seen[path]; !ok || old != st {
					hits = append(hits, path)
				}
			}
			sort.Strings(hits)
			if len(hits) > 0 {
				changed(hits)
			}
		}
		seen, first = now, false
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// matches reports whether a changed path is one a glob names.
func (s *Supervisor) matches(globs []string, path string) bool {
	match := s.Match
	if match == nil {
		// path.Match reads a pattern the way a shell does, which serves
		// for the simple globs a definition gives; the daemon sets the
		// one that reads them as a .gitignore does.
		match = func(pattern, name string) bool {
			ok, err := pathpkg.Match(pattern, name)
			return err == nil && ok
		}
	}
	for _, glob := range globs {
		if match(glob, path) {
			return true
		}
	}
	return false
}

// pollFiles walks root for the files the globs match, bounded by pollLimit
// and skipping what the owner says to skip.
func (s *Supervisor) pollFiles(globs []string, root string) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if s.Ignore != nil && s.Ignore(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if s.Ignore != nil && s.Ignore(rel, false) {
			return nil
		}
		if len(out) >= pollLimit {
			return filepath.SkipAll
		}
		if s.matches(globs, rel) {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// watchRestart replaces a run whose files changed, with the banner that
// says which file did it. It is what the user asked for, so it counts in
// nothing: not the restarts of a crashing process, and not its backoff.
func (s *Supervisor) watchRestart(project, name string, gen uint64, path string) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	_, p := s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed || p.handle == nil {
		s.mu.Unlock()
		return
	}
	h := p.handle
	if p.watch != nil {
		p.watch()
		p.watch = nil
	}
	p.proc.LastChange = path
	p.proc.Message = ""
	s.mu.Unlock()
	history := append([]byte(nil), h.History(true)...)
	history = append(history, []byte("\r\n\x1b[2m── restarted (changed: "+path+") ──\x1b[0m\r\n")...)
	// Restart's own path: the stop is not sticky, so the run comes back.
	s.mu.Lock()
	_, p = s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed {
		s.mu.Unlock()
		return
	}
	p.stopped = false
	// The replacement run starts with a generation of its own, so the old
	// run's exit, and its watch, end their work on a run that is gone.
	p.generation++
	newGen := p.generation
	p.handle, p.stop = nil, nil
	p.proc.Status = Starting
	p.proc.Since = time.Now().UnixMilli()
	s.mu.Unlock()
	s.runner.Remove(h.ID())
	s.changed()
	s.emit(Event{Project: project, Name: name, Kind: EventStarted})
	_ = s.startRun(project, name, p.def, p.dir, newGen, history, restartBase)
}

// setWatchMessage says what a watch did not manage, on the run it belongs
// to, without ending it.
func (s *Supervisor) setWatchMessage(project, name string, gen uint64, message string) {
	s.mu.Lock()
	_, p := s.find(project, name)
	if p != nil && p.generation == gen {
		p.proc.Message = message
	}
	s.mu.Unlock()
	s.changed()
}

// stamp is what tells a file changed: its size and modification time.
type stamp struct {
	size     int64
	modified int64
}

func stampOf(path string) stamp {
	info, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{size: info.Size(), modified: info.ModTime().UnixNano()}
}

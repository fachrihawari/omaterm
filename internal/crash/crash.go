// Package crash keeps what a crash of omaterm prints: Go's panic or fatal
// signal report, which goes to stderr, lost when the app was started from
// a launcher. Each process sends it to a log of its own, and the next run
// of the app tells the user, who can read it or send it on.
package crash

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Names of the processes that log their crashes.
var Names = []string{"app", "daemon"}

// LogPath is where a process's crashes go.
func LogPath(dir, name string) string { return filepath.Join(dir, "crash-"+name+".log") }

func runPath(dir, name string) string  { return filepath.Join(dir, "crash-"+name+".run") }
func seenPath(dir, name string) string { return filepath.Join(dir, "crash-"+name+".seen") }
func notedPath(dir, name string) string {
	return filepath.Join(dir, "crash-"+name+".noted")
}

// Watch sends this process's crash report to its log, as well as to
// stderr. A crash the log gained since the last run gets a line saying
// which run it was, then this run is written down for the next one.
func Watch(dir, name, version string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	note(dir, name)
	f, err := os.OpenFile(LogPath(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	// f stays open for the life of the process: the runtime writes to it
	// as it dies.
	if debug.SetCrashOutput(f, debug.CrashOptions{}) != nil {
		f.Close()
		return
	}
	run := fmt.Sprintf("omaterm %s %s, pid %d, started %s", name, version, os.Getpid(), time.Now().Format(time.RFC3339))
	os.WriteFile(runPath(dir, name), []byte(run), 0o600)
}

// note adds to a log that grew since it was last seen or noted the run
// that wrote it, so the report says which version crashed and when.
func note(dir, name string) {
	size := fileSize(LogPath(dir, name))
	if size <= max(readInt(seenPath(dir, name)), readInt(notedPath(dir, name))) {
		return
	}
	run, _ := os.ReadFile(runPath(dir, name))
	line := "\n--- the crash above: " + strings.TrimSpace(string(run)) + "\n\n"
	if len(run) == 0 {
		line = "\n--- the crash above: an earlier run of omaterm " + name + "\n\n"
	}
	if f, err := os.OpenFile(LogPath(dir, name), os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		f.WriteString(line)
		f.Close()
	}
	writeInt(notedPath(dir, name), fileSize(LogPath(dir, name)))
}

// Report is a crash the user has not been told of yet.
type Report struct {
	Name string // "app" or "daemon"
	Path string // its log
}

// Unseen returns the crashes logged since MarkSeen.
func Unseen(dir string) []Report {
	var out []Report
	for _, name := range Names {
		if fileSize(LogPath(dir, name)) > readInt(seenPath(dir, name)) {
			out = append(out, Report{Name: name, Path: LogPath(dir, name)})
		}
	}
	return out
}

// MarkSeen notes that the user was told of every crash logged so far.
func MarkSeen(dir string) {
	for _, name := range Names {
		writeInt(seenPath(dir, name), fileSize(LogPath(dir, name)))
	}
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

func readInt(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func writeInt(path string, n int64) {
	os.WriteFile(path, []byte(strconv.FormatInt(n, 10)), 0o600)
}

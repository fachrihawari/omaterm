package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Match is one search result.
type Match struct {
	Path  string // workspace-relative path
	Line  int    // 1-based line number
	Text  string // full line text (trimmed of trailing newline)
	Start int    // byte offset of match start in Text
	End   int    // byte offset of match end in Text
}

// MaxMatches caps how many matches a search returns.
const MaxMatches = 2000

// HasRipgrep reports whether rg is on PATH.
func HasRipgrep() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// SearchContent searches file contents under root for query.
// Uses rg --json when available; otherwise a Go walker.
func SearchContent(ctx context.Context, root, query string) ([]Match, error) {
	if query == "" {
		return nil, nil
	}
	if HasRipgrep() {
		return searchContentRG(ctx, root, query)
	}
	return searchContentGo(ctx, root, query)
}

type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber     int `json:"line_number"`
		AbsoluteOffset int `json:"absolute_offset"`
		Submatches     []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

func searchContentRG(ctx context.Context, root, query string) ([]Match, error) {
	// Stopping early, rg must stop too: left writing into a full pipe,
	// it blocked, and Wait with it, so a search with more than MaxMatches
	// results never returned.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{
		"--json", "--fixed-strings", "--ignore-case",
		"--max-count", strconv.Itoa(MaxMatches),
		// A minified bundle's line, megabytes long, ended the reading.
		"--max-columns", "2000", "--max-columns-preview",
	}
	if Protected(filepath.Join(root, "Library")) {
		args = append(args, "--glob", "!/Library/")
	}
	args = append(args, "--", query)
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var matches []Match
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var ev rgEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type != "match" {
			continue
		}
		rel, err := filepath.Rel(root, ev.Data.Path.Text)
		if err != nil {
			rel = ev.Data.Path.Text
		}
		text := strings.TrimRight(ev.Data.Lines.Text, "\n")
		for _, sm := range ev.Data.Submatches {
			matches = append(matches, Match{
				Path:  rel,
				Line:  ev.Data.LineNumber,
				Text:  text,
				Start: sm.Start,
				End:   sm.End,
			})
			if len(matches) >= MaxMatches {
				break
			}
		}
		if len(matches) >= MaxMatches {
			break
		}
	}
	cancel()
	_ = cmd.Wait()
	return matches, nil
}

// searchContentGo is the fallback walker-based content search.
func searchContentGo(ctx context.Context, root, query string) ([]Match, error) {
	lowerQuery := strings.ToLower(query)
	var matches []Match
	var mu sync.Mutex

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if indexSkipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") || Protected(path) {
				return filepath.SkipDir
			}
			return nil
		}
		mu.Lock()
		full := len(matches) >= MaxMatches
		mu.Unlock()
		if full {
			return filepath.SkipAll
		}
		searchFile(ctx, root, path, lowerQuery, &matches, &mu)
		return nil
	})
	return matches, err
}

func searchFile(ctx context.Context, root, path, lowerQuery string, matches *[]Match, mu *sync.Mutex) {
	data, err := os.ReadFile(path)
	if err != nil || IsBinary(data) {
		return
	}
	rel, _ := filepath.Rel(root, path)
	line := 0
	for len(data) > 0 {
		line++
		var l []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			l, data = data[:i], data[i+1:]
		} else {
			l, data = data, nil
		}
		if len(l) > 4096 {
			continue
		}
		lower := strings.ToLower(string(l))
		from := 0
		for {
			idx := strings.Index(lower[from:], lowerQuery)
			if idx < 0 {
				break
			}
			start := from + idx
			mu.Lock()
			if len(*matches) < MaxMatches {
				*matches = append(*matches, Match{
					Path:  rel,
					Line:  line,
					Text:  string(l),
					Start: start,
					End:   start + len(lowerQuery),
				})
			}
			mu.Unlock()
			from = start + len(lowerQuery)
			if from >= len(lower) {
				break
			}
		}
	}
}

// IsBinary reports whether data looks like a binary file: a NUL byte in
// its first 8 KB, as git decides.
func IsBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// ReadFileWindow returns lines of the file around centerLine (1-based),
// with at most radius lines before and after. Line numbers in the result
// are 1-based absolute.
func ReadFileWindow(root, rel string, centerLine, radius int) (lines []string, firstLine int, err error) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, 0, err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	first := centerLine - radius
	if first < 1 {
		first = 1
	}
	last := centerLine + radius
	if last > len(all) {
		last = len(all)
	}
	if first > last {
		return nil, 0, nil
	}
	return all[first-1 : last], first, nil
}

// ReadFile reads a whole file, truncated at maxBytes.
func ReadFile(root, rel string, maxBytes int64) (string, error) {
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}

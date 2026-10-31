package procs

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"omaterm/internal/paths"
)

// Trust is the user's trust in process definitions, per machine: a variant (Def.Variant) they reviewed, by project folder.
// The file is {"version":1,"projects":{"/abs/dir":{"<variant>":"<RFC3339 time>"}}}; it is read again only when it changed.
type Trust struct {
	path string

	mu     sync.Mutex
	stamp  trustStamp
	cached trustFile
	have   bool
}

// trustStamp tells whether the trust file changed since it was read.
type trustStamp struct {
	modified int64
	size     int64
}

// trustFile is the JSON that Trust keeps on disk.
type trustFile struct {
	Version  int                          `json:"version"`
	Projects map[string]map[string]string `json:"projects"`
}

// OpenTrust returns the trust kept in the file at path. The file is read when it is needed.
func OpenTrust(path string) *Trust { return &Trust{path: path} }

// TrustPath is the trust file in omaterm's config folder.
func TrustPath() string { return filepath.Join(paths.ConfigDir(), "trust.json") }

// Trusted reports whether the user has trusted the variant in the project at dir.
// A file that can't be read trusts nothing.
func (t *Trust) Trusted(dir, variant string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := t.read()
	if err != nil {
		return false
	}
	_, ok := f.Projects[filepath.Clean(dir)][variant]
	return ok
}

// Add records that the user has trusted the variant in the project at dir.
// It refuses to write over a trust file it can't read, so a broken file is
// never replaced without the user seeing it.
func (t *Trust) Add(dir, variant string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := t.read()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	t.have = false
	f.Version = 1
	if f.Projects == nil {
		f.Projects = map[string]map[string]string{}
	}
	dir = filepath.Clean(dir)
	if f.Projects[dir] == nil {
		f.Projects[dir] = map[string]string{}
	}
	f.Projects[dir][variant] = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	return writeAtomic(t.path, data, 0o600)
}

// read loads the trust file, or gives what it read before when the file
// is as it was: the supervisor asks for every definition it loads. A
// missing file gives an empty trust with fs.ErrNotExist. The lock is held.
func (t *Trust) read() (trustFile, error) {
	info, err := os.Stat(t.path)
	if err != nil {
		t.have = false
		return trustFile{}, err
	}
	stamp := trustStamp{modified: info.ModTime().UnixNano(), size: info.Size()}
	if t.have && stamp == t.stamp {
		return t.cached, nil
	}
	var f trustFile
	data, err := os.ReadFile(t.path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return trustFile{}, err
	}
	t.cached, t.stamp, t.have = f, stamp, true
	return f, nil
}

package procs

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"omaterm/internal/paths"
)

// Trust is the user's trust in process definitions, per machine: a variant (Def.Variant) they reviewed, by project folder.
type Trust struct{ path string } // JSON file: {"version":1,"projects":{"/abs/dir":{"<variant>":"<RFC3339 time>"}}}

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
	f, err := t.read()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
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

// read loads the trust file. A missing file gives an empty trust with fs.ErrNotExist.
func (t *Trust) read() (trustFile, error) {
	var f trustFile
	data, err := os.ReadFile(t.path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return trustFile{}, err
	}
	return f, nil
}

// Package procs holds a project's processes: the long-running dev commands
// (a dev server, a queue worker) a project defines in omaterm.yml, and the
// local ones kept in omaterm's own state. A command from the file runs only
// once the user has trusted that exact definition (see Trust).
package procs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Def is a process a project defines: what to run, where, and how omaterm keeps it running.
type Def struct {
	Name      string            `yaml:"-" json:"name"`
	Command   string            `yaml:"command" json:"command"`             // run by the login shell with -lc
	Dir       string            `yaml:"dir,omitempty" json:"dir,omitempty"` // relative to the project; inside it
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Port      int               `yaml:"port,omitempty" json:"port,omitempty"` // the port that means it is up
	Autostart bool              `yaml:"autostart,omitempty" json:"autostart,omitempty"`
	Restart   string            `yaml:"restart,omitempty" json:"restart,omitempty"` // never (default) | on-failure | always
	Watch     []string          `yaml:"watch,omitempty" json:"watch,omitempty"`     // globs; a change restarts it
	// URL is the name it is served at, before ".localhost": "shop" is
	// https://shop.localhost, through omaterm's Dev URLs proxy.
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Local bool   `yaml:"-" json:"-"` // kept in omaterm's state, not in the file
}

// nameRE is what a process is called: short, lowercase, and safe in a file name.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// labelRE is one label of a URL's name: a DNS label, lowercase.
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// variantKey is what a Variant hashes. Its fields are in a fixed order, and
// encoding/json sorts the env keys, so the JSON is the same for the same def.
type variantKey struct {
	Command string            `json:"command"`
	Dir     string            `json:"dir"`
	Env     map[string]string `json:"env"`
	Port    int               `json:"port"`
	Restart string            `json:"restart"`
	Watch   []string          `json:"watch"`
	// URL is left out when empty, so that a def without one keeps the
	// variant, and the trust, it had before URLs.
	URL string `json:"url,omitempty"`
}

// Variant is a hash of what the process runs, so that trust follows the
// command and not the name. Name, Autostart and Local are left out: renaming
// a process or turning autostart on keeps the trust the user gave it.
func (d Def) Variant() string {
	k := variantKey{
		Command: d.Command,
		Dir:     cleanDir(d.Dir),
		Port:    d.Port,
		Restart: restartPolicy(d.Restart),
		URL:     d.URL,
	}
	// An empty env or watch list is the same as none, so it hashes as null.
	if len(d.Env) > 0 {
		k.Env = d.Env
	}
	if len(d.Watch) > 0 {
		k.Watch = append([]string(nil), d.Watch...)
		sort.Strings(k.Watch)
	}
	data, _ := json.Marshal(k) // the fields are plain strings, ints and maps: it cannot fail
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// restartPolicy makes the default restart policy and "never" the same, so a
// def that leaves restart out has the variant of one that says never.
func restartPolicy(restart string) string {
	if restart == "" {
		return "never"
	}
	return restart
}

// cleanDir makes "./web", "web/" and "" the same folder, the project itself
// being "".
func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if dir == "." {
		return ""
	}
	return filepath.ToSlash(dir)
}

// Validate reports whether the def can run in the project at root. It checks
// the name, the command, that dir stays inside the project, the port and
// the restart policy.
func (d Def) Validate(root string) error {
	if !nameRE.MatchString(d.Name) {
		return fmt.Errorf("the name %q must be 1 to 32 of a-z, 0-9, - or _, starting with a letter or digit", d.Name)
	}
	if strings.TrimSpace(d.Command) == "" {
		return errors.New("the command is empty")
	}
	if d.Dir != "" {
		// An absolute dir would be joined onto root as if it were relative, so refuse it first.
		if filepath.IsAbs(d.Dir) {
			return errors.New("the dir must be relative to the project")
		}
		inside, err := filepath.Rel(filepath.Clean(root), filepath.Join(root, d.Dir))
		if err != nil || !filepath.IsLocal(inside) {
			return errors.New("the dir leaves the project")
		}
	}
	if d.Port < 0 || d.Port > 65535 {
		return fmt.Errorf("the port %d must be 0 or 1 to 65535", d.Port)
	}
	switch d.Restart {
	case "", "never", "on-failure", "always":
	default:
		return fmt.Errorf("the restart %q must be never, on-failure or always", d.Restart)
	}
	if d.URL != "" {
		if err := ValidURL(d.URL); err != nil {
			return err
		}
	}
	return nil
}

// ValidURL reports whether name can be a URL's name: one to three DNS
// labels, lowercase, before ".localhost", as "shop" or "api.shop".
func ValidURL(name string) error {
	labels := strings.Split(name, ".")
	if len(labels) > 3 {
		return fmt.Errorf("the url %q has more than three parts", name)
	}
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return fmt.Errorf("the url %q must be parts of a-z, 0-9 and -, joined by dots, as shop or api.shop", name)
		}
	}
	if labels[len(labels)-1] == "localhost" {
		return fmt.Errorf("the url %q leaves out .localhost: omaterm adds it", name)
	}
	return nil
}

// Host is the host a URL's name is served at.
func Host(name string) string { return name + ".localhost" }

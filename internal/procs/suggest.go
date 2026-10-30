package procs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// suggestScripts are the package.json scripts worth a suggestion, best first.
var suggestScripts = []string{"dev", "start", "serve", "watch", "preview", "storybook", "test:watch"}

// suggestTargets are the Makefile, justfile and Taskfile names worth a suggestion, best first.
var suggestTargets = []string{"dev", "serve", "watch", "run"}

// suggestTools are the tools a script can run, with the port each serves on by default.
var suggestTools = []struct {
	name string
	port int
}{
	{"storybook", 6006},
	{"astro", 4321},
	{"next", 3000},
	{"nuxt", 3000},
	{"vite", 5173},
	{"remix", 5173},
	{"react-router", 5173},
	{"svelte-kit", 5173},
}

// portFlagRE finds a port a command names: --port 3001, -p 3001, PORT=3001.
var portFlagRE = regexp.MustCompile(`(?:--port[= ]|-p |PORT=)(\d+)`)

// Suggest guesses the processes the project at root could define, from
// the files it has: nothing runs, and nothing is written. Each is a Def
// the user can take as it is, or edit. The best guesses come first.
func Suggest(root string) []Def {
	var defs []Def
	defs = append(defs, packageScripts(root)...)
	defs = append(defs, procfileDefs(root, "Procfile.dev")...)
	defs = append(defs, procfileDefs(root, "Procfile")...)
	defs = append(defs, taskDefs(root)...)
	defs = append(defs, composeDefs(root)...)
	defs = append(defs, frameworkDefs(root)...)
	for _, d := range ImportSolo(root) {
		// A suggestion runs in the project, not in Solo's settings for it.
		d.Dir, d.Autostart, d.Restart = "", false, ""
		defs = append(defs, d)
	}

	var out []Def
	seen := map[string]bool{}
	for _, d := range defs {
		d.Name = suggestName(d.Name)
		if seen[d.Name] || d.Validate(root) != nil {
			continue
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	return out
}

// packageScripts suggests the scripts in package.json that run a server or a watcher.
func packageScripts(root string) []Def {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		PackageManager string            `json:"packageManager"`
		Scripts        map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	pm := packageManager(root, pkg.PackageManager)
	var defs []Def
	for _, script := range suggestScripts {
		text, ok := pkg.Scripts[script]
		if !ok {
			continue
		}
		defs = append(defs, Def{Name: script, Command: pm + " run " + script, Port: guessPort(text)})
	}
	return defs
}

// packageManager is the package manager the project uses: the one its
// packageManager field names, else the one its lock file belongs to.
func packageManager(root, field string) string {
	name, _, _ := strings.Cut(field, "@")
	switch name {
	case "npm", "pnpm", "yarn", "bun":
		return name
	}
	switch {
	case fileExists(root, "bun.lock"), fileExists(root, "bun.lockb"):
		return "bun"
	case fileExists(root, "pnpm-lock.yaml"):
		return "pnpm"
	case fileExists(root, "yarn.lock"):
		return "yarn"
	}
	return "npm"
}

// procfileDefs suggests the lines of a Procfile, which are name: command.
func procfileDefs(root, file string) []Def {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil
	}
	var defs []Def
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		name, command, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		d := Def{Name: strings.TrimSpace(name), Command: strings.TrimSpace(command)}
		// A $PORT is picked when the process starts, so no port is guessed for it.
		if !strings.Contains(d.Command, "$PORT") {
			d.Port = guessPort(d.Command)
		}
		defs = append(defs, d)
	}
	return defs
}

// taskDefs suggests the Makefile targets, justfile recipes and Taskfile tasks
// that are named in suggestTargets.
func taskDefs(root string) []Def {
	var defs []Def
	if data, err := os.ReadFile(filepath.Join(root, "Makefile")); err == nil {
		for _, t := range suggestTargets {
			re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(t) + `\s*:(?:[^=\n]|$)`)
			if re.Match(data) {
				defs = append(defs, Def{Name: t, Command: "make " + t})
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "justfile")); err == nil {
		for _, t := range suggestTargets {
			re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(t) + `(?:\s[^:=\n]*)?:(?:[^=\n]|$)`)
			if re.Match(data) {
				defs = append(defs, Def{Name: t, Command: "just " + t})
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "Taskfile.yml")); err == nil {
		var tf struct {
			Tasks map[string]any `yaml:"tasks"`
		}
		if yaml.Unmarshal(data, &tf) == nil {
			for _, t := range suggestTargets {
				if _, ok := tf.Tasks[t]; ok {
					defs = append(defs, Def{Name: t, Command: "task " + t})
				}
			}
		}
	}
	return defs
}

// composeDefs suggests the stack of the first compose file the project has.
func composeDefs(root string) []Def {
	for _, file := range []string{"docker-compose.yml", "compose.yml", "compose.yaml"} {
		if fileExists(root, file) {
			return []Def{{Name: "compose", Command: "docker compose up"}}
		}
	}
	return nil
}

// frameworkDefs suggests the servers of the frameworks the project's root
// shows it uses: Laravel, Rails, Django and Go.
func frameworkDefs(root string) []Def {
	var defs []Def
	if fileExists(root, "artisan") {
		defs = append(defs,
			Def{Name: "serve", Command: "php artisan serve", Port: 8000},
			Def{Name: "queue", Command: "php artisan queue:listen"},
		)
	}
	if fileExists(root, "bin", "rails") {
		defs = append(defs, Def{Name: "rails", Command: "bin/rails server", Port: 3000})
	}
	if fileExists(root, "manage.py") {
		defs = append(defs, Def{Name: "django", Command: "python manage.py runserver", Port: 8000})
	}
	if fileExists(root, "go.mod") && fileExists(root, "main.go") {
		defs = append(defs, Def{Name: "go", Command: "go run ."})
	}
	return defs
}

// guessPort is the port a command serves on: the one it names, else the
// default of the tool it runs. Zero when neither is known.
func guessPort(command string) int {
	if m := portFlagRE.FindStringSubmatch(command); m != nil {
		if port, err := strconv.Atoi(m[1]); err == nil && port >= 1 && port <= 65535 {
			return port
		}
	}
	for field := range strings.FieldsSeq(command) {
		base := filepath.Base(field)
		for _, tool := range suggestTools {
			if base == tool.name {
				return tool.port
			}
		}
	}
	return 0
}

// suggestName makes a name Validate accepts: lowercase, with each character
// it doesn't allow turned into "-". It is "" when nothing usable is left.
func suggestName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.TrimLeft(b.String(), "-_")
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// fileExists reports whether the file at path, joined onto the parts, is there and is not a folder.
func fileExists(root string, parts ...string) bool {
	info, err := os.Stat(filepath.Join(append([]string{root}, parts...)...))
	return err == nil && !info.IsDir()
}

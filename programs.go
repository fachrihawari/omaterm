package main

import (
	"embed"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
)

//go:embed icons/programs/*.svg
var programSVGs embed.FS

//go:embed icons/maximize-2.svg
var iconMaximizeSVG []byte

//go:embed icons/minimize-2.svg
var iconMinimizeSVG []byte

//go:embed icons/server.svg
var iconServerSVG []byte

//go:embed icons/activity.svg
var iconActivitySVG []byte

//go:embed icons/database.svg
var iconDatabaseSVG []byte

//go:embed icons/bell.svg
var iconBellSVG []byte

var (
	iconMaximize = ui.MustParseSVG(iconMaximizeSVG)
	iconMinimize = ui.MustParseSVG(iconMinimizeSVG)
	iconServer   = ui.MustParseSVG(iconServerSVG)
	iconActivity = ui.MustParseSVG(iconActivitySVG)
	iconDatabase = ui.MustParseSVG(iconDatabaseSVG)
	iconBell     = ui.MustParseSVG(iconBellSVG)
)

// programIcons maps a program's name to its logo: Simple Icons' for those
// that have one, a Lucide icon for kinds of program.
var programIcons = map[string]*ui.SVG{}

func init() {
	parsed := map[string]*ui.SVG{}
	logo := func(slug string) *ui.SVG {
		if ic := parsed[slug]; ic != nil {
			return ic
		}
		data, err := programSVGs.ReadFile(path.Join("icons/programs", "si-"+slug+".svg"))
		if err != nil {
			panic(err)
		}
		parsed[slug] = ui.MustParseSVG(data)
		return parsed[slug]
	}
	for name, slug := range map[string]string{
		"claude": "claude", "codex": "openai", "gemini": "googlegemini", "copilot": "githubcopilot",
		"cursor-agent": "cursor", "amp": "amp", "opencode": "opencode", "cline": "cline", "qwen": "qwen",
		"kimi": "kimi", "node": "nodedotjs", "npm": "nodedotjs", "npx": "nodedotjs", "pnpm": "nodedotjs",
		"bun": "bun", "bunx": "bun", "deno": "deno", "python": "python", "python3": "python", "uv": "python",
		"pip": "python", "go": "go", "cargo": "rust", "rustc": "rust", "docker": "docker",
		"docker-compose": "docker", "git": "git", "nvim": "neovim", "vim": "vim", "vi": "vim",
	} {
		programIcons[name] = logo(slug)
	}
	for _, n := range []string{"ssh", "mosh", "et"} {
		programIcons[n] = iconServer
	}
	for _, n := range []string{"htop", "btop", "top", "bottom", "btm"} {
		programIcons[n] = iconActivity
	}
	for _, n := range []string{"psql", "mysql", "redis-cli", "sqlite3", "mongosh", "pgcli"} {
		programIcons[n] = iconDatabase
	}
	for _, n := range []string{"lazygit", "tig", "gitui"} {
		programIcons[n] = iconGitBranch
	}
	for _, n := range []string{"less", "man", "more", "bat"} {
		programIcons[n] = iconFileText
	}
}

// programIcon is the icon of a program, by its name: the terminal's when
// it has none of its own, as a shell's.
func programIcon(name string) *ui.SVG {
	if ic, ok := programIcons[strings.ToLower(name)]; ok {
		return ic
	}
	return iconTerminal
}

// programName is how a pane names what it runs: an agent by its own name,
// any other program by its command's.
func programName(proc, ag string) string {
	if ag != "" {
		return agent.DisplayName(ag)
	}
	return proc
}

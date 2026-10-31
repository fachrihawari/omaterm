package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/shellcmd"
)

// What the app's Settings window shows and changes of what the omaterm
// command installs: the command itself, the daemon's service, and the
// agents' hooks and skills.

// cliLink is where `omaterm cli install` puts the command. On Windows
// that is omaterm's own folder, where the app keeps omaterm.exe for panes
// and hooks: a link in ~/.local/bin would need Developer Mode.
func cliLink() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(paths.BinDir(), "omaterm.exe")
	}
	return filepath.Join(paths.Home(), ".local", "bin", "omaterm")
}

// CLIStatus is where the omaterm command is linked, and whether it is.
func CLIStatus() (where string, installed bool) {
	link := cliLink()
	if runtime.GOOS == "windows" {
		_, err := os.Stat(link)
		return shortDir(link), err == nil
	}
	_, err := os.Readlink(link)
	return shortDir(link), err == nil
}

// CLIPathHint says what to add to the shell's profile when the folder the
// command is linked into isn't on PATH, as on a Mac, whose PATH has no
// ~/.local/bin; "" when it is on PATH.
func CLIPathHint() string {
	return cliPathHint(filepath.Dir(cliLink()), os.Getenv("PATH"), shellcmd.Login(), runtime.GOOS)
}

func cliPathHint(dir, path, shell, goos string) string {
	for _, p := range filepath.SplitList(path) {
		if p != "" && filepath.Clean(p) == dir {
			return ""
		}
	}
	lead := shortDir(dir) + " is not on your PATH, so other terminals don't find omaterm. "
	if goos == "windows" {
		// The user's PATH needs no administrator. The folder is quoted for
		// pwsh, where a $ in a double-quoted string would expand.
		quoted := "'" + strings.ReplaceAll(dir, "'", "''") + "'"
		return lead + `Run in PowerShell: [Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";" + ` + quoted + `, "User")` +
			", or add it under Settings → System → About → Advanced system settings → Environment Variables."
	}
	profile := "~/.profile"
	switch shellcmd.Name(shell) {
	case "fish":
		return lead + "Run: fish_add_path ~/.local/bin"
	case "zsh":
		profile = "~/.zprofile"
	case "bash":
		// A login bash reads ~/.bash_profile alone when it exists, and
		// Terminal.app starts login shells; on Linux, ~/.profile is the
		// one desktops read.
		if goos == "darwin" {
			profile = "~/.bash_profile"
		}
	}
	return lead + "Add this line to " + profile + `: export PATH="$HOME/.local/bin:$PATH"`
}

// UninstallCLI removes the omaterm command's link, as `omaterm cli
// uninstall` does; a file of its own there is left alone.
func UninstallCLI() error {
	link := cliLink()
	if st, err := os.Lstat(link); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(link)
	}
	return nil
}

// InstallService installs and starts the session daemon's systemd user
// unit, as `omaterm daemon install-service` does.
func InstallService() error { return installService() }

// UninstallService stops and removes the unit, as `omaterm daemon
// uninstall-service` does.
func UninstallService() error {
	if !daemon.ServiceInstalled() {
		return nil
	}
	// The unit goes before the daemon stops: an app reconnecting at the
	// stop starts the service while its unit is there, which brought the
	// service straight back.
	exec.Command("systemctl", "--user", "disable", daemon.ServiceName()).Run()
	if err := os.Remove(daemon.UnitPath()); err != nil {
		return err
	}
	exec.Command("systemctl", "--user", "stop", daemon.ServiceName()).Run()
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// settingsCmd opens the app's Settings window, on a page when named: it
// starts the app with --settings, which an open app takes over.
func settingsCmd(args []string) error {
	page := ""
	if len(args) > 0 {
		page = args[0]
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--settings="+page)
	cmd.Dir = paths.Home()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// HookAgents are the agents omaterm has a hook or plugin for.
var HookAgents = []string{"claude", "codex", "pi", "opencode", "agy"}

// AgentIntegration is what omaterm installed in one agent: its hook or
// plugin, and the skill it reads, each "current", "outdated" or
// "missing". Present says the agent is installed here, or that it is
// Claude Code, which is always offered.
type AgentIntegration struct {
	Agent                string
	HookPath, HookState  string
	SkillDir, SkillState string
	Present              bool
}

// Integration is the state of one agent's integration.
func Integration(agent string) AgentIntegration {
	it := AgentIntegration{Agent: agent, HookState: "missing"}
	for _, h := range agentHooks(agent) {
		if h.name() == agent {
			it.HookPath, it.HookState = h.path(), h.state()
		}
	}
	// agentHooks(agent) names an agent whether or not it is installed;
	// the shared list leaves out the ones that are not.
	for _, h := range agentHooks("") {
		if h.name() == agent {
			it.Present = true
		}
	}
	group := skillGroup(agent)
	it.SkillDir = skillDirs(group)[group]
	it.SkillState = skillState(it.SkillDir)
	return it
}

// Installed tells whether omaterm's integration is in the agent at all.
func (it AgentIntegration) Installed() bool { return it.HookState != "missing" }

// Current tells whether all of the integration is installed and current.
func (it AgentIntegration) Current() bool {
	return it.HookState == "current" && it.SkillState == "current"
}

// skillGroup is the skill an agent reads: Claude Code's own,
// Antigravity's own, or the one Codex, OpenCode and Pi share.
func skillGroup(agent string) string {
	switch agent {
	case "claude", "agy":
		return agent
	}
	return "agents"
}

// SetupAgent installs, or updates, an agent's hook and the skill it reads.
func SetupAgent(agent string) error {
	group := skillGroup(agent)
	if err := writeSkill(skillDirs(group)[group]); err != nil {
		return err
	}
	for _, h := range agentHooks(agent) {
		if h.name() == agent {
			return h.install()
		}
	}
	return errors.New("no integration for " + agent)
}

// RemoveAgent removes an agent's hook, and the skill it reads once no
// other agent with a hook reads it.
func RemoveAgent(agent string) error {
	for _, h := range agentHooks(agent) {
		if h.name() == agent {
			if err := h.remove(); err != nil {
				return err
			}
		}
	}
	group := skillGroup(agent)
	for _, other := range HookAgents {
		if other != agent && skillGroup(other) == group && Integration(other).HookState != "missing" {
			return nil
		}
	}
	return os.RemoveAll(skillDirs(group)[group])
}

// ShortPath shows a path under the home directory with ~.
func ShortPath(p string) string { return shortDir(p) }

// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/cli/hook_test.go.

package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func testHook(t *testing.T, settings string) agentHook {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "omaterm home"))
	h := agentHook{"claude", filepath.Join(t.TempDir(), "settings.json")}
	if settings != "" {
		if err := os.WriteFile(h.file, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Installing keeps the rest of the settings, in their order, and the
// other hooks; installing again changes nothing; removing takes only
// omaterm's out.
func TestHookInstallKeepsSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command is quoted for pwsh and cmd there, not for sh")
	}
	h := testHook(t, `{
  "model": "opus",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
    "SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "echo hi"}]}]
  },
  "env": {"A": "1"}
}
`)
	if st := h.state(); st != "missing" {
		t.Fatalf("state %s before install", st)
	}
	if err := h.install(); err != nil {
		t.Fatal(err)
	}
	got := read(t, h.file)
	if h.state() != "current" {
		t.Fatalf("state %s after install:\n%s", h.state(), got)
	}
	if i, j, k := strings.Index(got, `"model"`), strings.Index(got, `"hooks"`), strings.Index(got, `"env"`); !(i < j && j < k) {
		t.Fatalf("keys out of order:\n%s", got)
	}
	for _, want := range []string{`"say done"`, `"echo hi"`, `"matcher": "startup"`, `"timeout": 10`, "omaterm home/omaterm/bin/omaterm' hook claude"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %s in:\n%s", want, got)
		}
	}
	if err := h.install(); err != nil {
		t.Fatal(err)
	}
	if again := read(t, h.file); again != got {
		t.Fatalf("installing again changed it:\n%s", again)
	}
	if err := h.remove(); err != nil {
		t.Fatal(err)
	}
	removed := read(t, h.file)
	if strings.Contains(removed, "omaterm") || !strings.Contains(removed, `"echo hi"`) || !strings.Contains(removed, `"say done"`) {
		t.Fatalf("after remove:\n%s", removed)
	}
	if h.state() != "missing" {
		t.Fatalf("state %s after remove", h.state())
	}
}

// Another omaterm's hook, as of a moved OMATERM_HOME, is outdated, and setup
// replaces it.
func TestHookReplacesOutdated(t *testing.T) {
	h := testHook(t, `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "'/old/omaterm/bin/omaterm' hook claude", "timeout": 10}]}]}}`)
	if st := h.state(); st != "outdated" {
		t.Fatalf("state %s, want outdated", st)
	}
	if err := h.install(); err != nil {
		t.Fatal(err)
	}
	got := read(t, h.file)
	if strings.Contains(got, "/old/omaterm") || strings.Count(got, "hook claude") != 1 {
		t.Fatalf("after install:\n%s", got)
	}
}

// Without a settings file, install makes one, and remove takes away what
// it made.
func TestHookNewFile(t *testing.T) {
	h := testHook(t, "")
	if err := h.install(); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(read(t, h.file)), &v); err != nil {
		t.Fatal(err)
	}
	if err := h.remove(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(read(t, h.file)); got != "{}" {
		t.Fatalf("after remove: %s", got)
	}
}

// Settings that aren't JSON are left alone.
func TestHookRefusesBadSettings(t *testing.T) {
	h := testHook(t, "{ // a comment\n}")
	if err := h.install(); err == nil {
		t.Fatal("installed into settings it can't read")
	}
	if got := read(t, h.file); got != "{ // a comment\n}" {
		t.Fatalf("changed: %s", got)
	}
}

// Pi's and OpenCode's plugins go in when the agent is installed, run
// omaterm's own omaterm command, and come out whole.
func TestAgentPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(env, "")
	}
	names := func() (out []string) {
		for _, it := range agentHooks("") {
			out = append(out, it.name())
		}
		return
	}
	if got := strings.Join(names(), ","); got != "claude" {
		t.Fatalf("with no agents installed: %s", got)
	}
	os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	if got := strings.Join(names(), ","); got != "claude,pi,opencode" {
		t.Fatalf("with Pi and OpenCode: %s", got)
	}
	for _, it := range agentHooks("agents") {
		if it.state() != "missing" {
			t.Fatalf("%s: %s before install", it.name(), it.state())
		}
		if err := it.install(); err != nil {
			t.Fatal(err)
		}
		if it.state() != "current" {
			t.Fatalf("%s: %s after install", it.name(), it.state())
		}
		p := it.(agentPlugin)
		for name := range p.files {
			b := read(t, filepath.Join(p.dir, name))
			if strings.Contains(b, "__OMATERM__") || name != "package.json" && !strings.Contains(b, filepath.Join(home, "state", "omaterm", "bin", "omaterm")) {
				t.Fatalf("%s/%s runs no omaterm:\n%s", it.name(), name, b)
			}
		}
		if err := it.remove(); err != nil {
			t.Fatal(err)
		}
		if it.state() != "missing" {
			t.Fatalf("%s: %s after remove", it.name(), it.state())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "omaterm")); !os.IsNotExist(err) {
		t.Fatal("OpenCode's plugin directory stayed")
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions")); err != nil {
		t.Fatal("Pi's extensions directory went with omaterm's extension")
	}
}

// Writing an agent's settings through a link writes its target: the link
// a dotfiles manager keeps became a plain file it no longer tracked.
func TestWriteThroughLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("{}"), 0o644)
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(link, []byte(`{"hooks": {}}`)); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if data, _ := os.ReadFile(target); string(data) != `{"hooks": {}}` {
		t.Errorf("the target holds %q", data)
	}
}

// Antigravity reads skills and hooks from a folder of its own: setup puts
// omaterm's skill there and omaterm's entry in its hooks.json, beside the
// user's own hooks, once Antigravity is installed; removing it takes only
// omaterm's away.
func TestAntigravityIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command is quoted for pwsh and cmd there, not for sh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(env, "")
	}
	config := filepath.Join(home, ".gemini", "config")
	dir := filepath.Join(config, "skills", "omaterm")
	hooksFile := filepath.Join(config, "hooks.json")
	if _, ok := skillDirs("")["agy"]; ok || slices.Contains(SkillGroups(), "agy") {
		t.Fatal("Antigravity's skill offered where Antigravity isn't installed")
	}
	if it := Integration("agy"); it.Present || it.Installed() {
		t.Fatalf("not installed: %+v", it)
	}
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := `{
  "lint": {
    "PostToolUse": [{"matcher": "run_command", "hooks": [{"command": "./lint.sh"}]}]
  }
}
`
	if err := os.WriteFile(hooksFile, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := skillDirs(agentArg("Antigravity")); len(got) != 1 || got["agy"] != dir {
		t.Fatalf("--agent antigravity: %v", got)
	}
	for _, h := range hooksFor("agents") {
		if h.name() == "agy" {
			t.Fatal("the shared skill's setup takes Antigravity's hook along")
		}
	}
	if err := SetupAgent("agy"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err != nil || string(b) != skillGuide {
		t.Fatalf("skill not written: %v", err)
	}
	b, _ := os.ReadFile(hooksFile)
	var hooks map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &hooks); err != nil {
		t.Fatalf("hooks.json: %v\n%s", err, b)
	}
	if hooks["lint"] == nil || hooks["omaterm"]["SessionStart"] == nil || hooks["omaterm"]["PreInvocation"] == nil {
		t.Fatalf("hooks.json after setup:\n%s", b)
	}
	var handlers []struct{ Type, Command string }
	json.Unmarshal(hooks["omaterm"]["SessionStart"], &handlers)
	if len(handlers) != 1 || handlers[0].Type != "command" || !strings.HasSuffix(handlers[0].Command, " hook agy SessionStart") {
		t.Fatalf("SessionStart handlers: %+v", handlers)
	}
	it := Integration("agy")
	if !it.Present || !it.Current() {
		t.Fatalf("after setup: %+v", it)
	}
	// Set up again: nothing changes.
	if err := SetupAgent("agy"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(hooksFile); string(again) != string(b) {
		t.Fatalf("a second setup changed hooks.json:\n%s", again)
	}
	// Another omaterm's command line is outdated.
	os.WriteFile(hooksFile, []byte(strings.ReplaceAll(string(b), "omaterm' hook", "omaterm-old' hook")), 0o644)
	if st := (agyHook{file: hooksFile}).state(); st != "outdated" {
		t.Fatalf("another command line: %s", st)
	}
	if err := RemoveAgent("agy"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(hooksFile)
	hooks = nil
	json.Unmarshal(b, &hooks)
	if hooks["omaterm"] != nil || hooks["lint"] == nil {
		t.Fatalf("hooks.json after remove:\n%s", b)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the skill stayed after removing the integration")
	}
}

// Antigravity's hook always answers with a JSON object, as its hooks
// must, and says nothing outside omaterm.
func TestAgyHookOutsideOmaterm(t *testing.T) {
	t.Setenv("OMATERM_PANE", "")
	for _, event := range []string{"SessionStart", "PreInvocation"} {
		out := captureStdout(t, func() {
			withStdin(t, `{"conversationId":"1fb8bf09-f856-487f-bfae-67288362eb5b","invocationNum":0}`, func() {
				if err := hookCmd([]string{"agy", event}); err != nil {
					t.Fatal(err)
				}
			})
		})
		if strings.TrimSpace(out) != "{}" {
			t.Fatalf("%s outside omaterm printed %q", event, out)
		}
	}
}

// withStdin runs fn with in as the process's standard input.
func withStdin(t *testing.T, in string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(in)
	w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close() }()
	fn()
}

// captureStdout is what fn writes to the standard output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	w.Close()
	b, _ := io.ReadAll(r)
	r.Close()
	return string(b)
}

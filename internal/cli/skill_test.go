package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skill tells agents where a process's Dev URL is, so they open it
// rather than a localhost port.
func TestSkillMentionsLink(t *testing.T) {
	if !strings.Contains(skillGuide, "omaterm ps --json") || !strings.Contains(skillGuide, "link") {
		t.Fatalf("the skill does not name ps --json's link:\n%s", skillGuide)
	}
}

// As the app starts, a skill an older omaterm installed is brought up to
// date, and its hook too; one never installed stays missing.
func TestRefreshOutdated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	claude := filepath.Join(home, ".claude", "skills", "omaterm")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "SKILL.md"), []byte("an older skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	old := `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "'/old/omaterm' hook claude"}]}]}}`
	if err := os.WriteFile(settings, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	RefreshOutdated()
	if skillState(claude) != "current" {
		t.Error("the outdated skill wasn't updated")
	}
	if got := read(t, settings); !strings.Contains(got, "PreToolUse") || strings.Contains(got, "/old/omaterm") {
		t.Errorf("the outdated hook wasn't updated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "omaterm")); err == nil {
		t.Error("the shared skill, never installed, was")
	}
}

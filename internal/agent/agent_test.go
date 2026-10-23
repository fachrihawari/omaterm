package agent

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"claude", []string{"claude"}, "claude"},
		{"node", []string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}, "codex"},
		{"node", []string{"node", "/x/@anthropic-ai/claude-code/cli.js"}, "claude"},
		{"cursor-agent", nil, "cursor-agent"},
		{"pi-mono", nil, ""}, // pi's prefix rule is off: too many programs start with pi
		{"pi", nil, "pi"},
		{"zsh", []string{"-zsh"}, ""},
		{"gemini-cli", nil, "gemini"},
	}
	for _, c := range cases {
		if got := Detect(c.name, c.args); got != c.want {
			t.Errorf("Detect(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestTitles(t *testing.T) {
	if !TitleBusy("⠋ Building") || !TitleBusy("◐ thinking") || TitleBusy("✳ Claude Code") || TitleBusy("zsh") {
		t.Error("TitleBusy")
	}
	if got := CleanTitle("✳ ⠋ Claude Code"); got != "Claude Code" {
		t.Errorf("CleanTitle %q", got)
	}
}

func TestResumeCommand(t *testing.T) {
	if got := ResumeCommand(&Session{"claude", "0b6f-1a"}); len(got) != 3 || got[2] != "0b6f-1a" {
		t.Errorf("claude: %q", got)
	}
	if got := ResumeCommand(&Session{"codex", "abc"}); got[1] != "resume" {
		t.Errorf("codex: %q", got)
	}
	for _, bad := range []string{"a;rm -rf ~", "$(x)", "", string(make([]byte, 129))} {
		if ResumeCommand(&Session{"claude", bad}) != nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if ResumeCommand(&Session{"aider", "abc"}) != nil || ResumeCommand(nil) != nil {
		t.Error("no resume for unknown agents")
	}
}

// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/detect/detect_test.go.

package detect

import (
	"os"
	"strings"
	"testing"
)

func TestManifestsCompile(t *testing.T) {
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"claude", "codex", "gemini", "opencode", "amp", "cursor-agent", "droid", "qwen", "copilot", "kiro-cli", "cline", "kimi", "pi"} {
		if !Known(a) {
			t.Errorf("no rules for %s", a)
		}
	}
	if Known("bash") {
		t.Error("rules for bash")
	}
}

func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Claude Code's screens, as kero read printed them.
func TestClaude(t *testing.T) {
	cases := []struct {
		name  string
		in    Input
		state State
	}{
		{"at its prompt", Input{Screen: screen(t, "claude-idle.txt"), Title: "✳ Claude Code"}, Idle},
		{"thinking", Input{Screen: screen(t, "claude-working.txt"), Title: "⠂ Probe"}, Working},
		{"thinking, the title not yet turning", Input{Screen: screen(t, "claude-working.txt")}, Working},
		{"asking to run a command", Input{Screen: screen(t, "claude-blocked.txt"), Title: "✳ Probe"}, Blocked},
		{"its turn done", Input{Screen: screen(t, "claude-done.txt"), Title: "✳ Claude Code"}, Idle},
		// The prompt shows, but the title turns: a turn that started.
		{"title turning", Input{Screen: screen(t, "claude-idle.txt"), Title: "⠐ Probe"}, Working},
		// A redraw at another width shows the same: still idle.
		{"rewrapped", Input{Screen: strings.ReplaceAll(screen(t, "claude-idle.txt"), "────", "──"), Title: "✳ Claude Code"}, Idle},
	}
	for _, c := range cases {
		if got := Detect("claude", c.in); got.State != c.state {
			t.Errorf("%s: %s (rule %q), want %s", c.name, got.State, got.Rule, c.state)
		}
	}
}

func TestRegions(t *testing.T) {
	s := "a\n\nb\n───\nbox\n───\nfoot\n"
	for spec, want := range map[string]string{
		"bottom_non_empty_lines(2)":  "───\nfoot\n",
		"bottom_lines(1)":            "foot\n",
		"top_non_empty_lines(2)":     "a\n\nb\n",
		"after_last_horizontal_rule": "foot\n",
		"prompt_box_body":            "box\n",
		"above_prompt_box":           "a\n\nb\n",
		"osc_title":                  "T",
		"osc_progress":               "4;3",
	} {
		if got := region(Input{Screen: s, Title: "T", Progress: "4;3"}, spec); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
	}
}

// Kiro's progress turns while it works.
func TestProgress(t *testing.T) {
	if got := Detect("kiro-cli", Input{Progress: "4;3"}); got.State != Working {
		t.Errorf("progress 4;3: %s (rule %q), want working", got.State, got.Rule)
	}
	if got := Detect("kiro-cli", Input{Progress: "4;0"}); got.State != Idle {
		t.Errorf("progress 4;0: %s (rule %q), want idle", got.State, got.Rule)
	}
}

// Codex's hooks waiting for review, over its prompt as it starts: the
// user's to answer, and nothing typed reaches the prompt meanwhile.
func TestCodexHooksReview(t *testing.T) {
	in := Input{Screen: screen(t, "codex-hooks-review.txt"), Title: "repo"}
	if got := Detect("codex", in); got.State != Blocked {
		t.Fatalf("%s (rule %q), want blocked", got.State, got.Rule)
	}
	// Closed, Codex is at its prompt.
	in.Screen = "  >_ OpenAI Codex (v0.163.0)\n\n› Ask Codex to do anything\n\n  gpt-6 medium · ~/repo\n"
	if got := Detect("codex", in); got.State != Idle {
		t.Fatalf("closed: %s (rule %q)", got.State, got.Rule)
	}
}

// Claude Code's last word on its turn is the last paragraph it said,
// not a tool call, and only once it is at its prompt with one.
func TestClaudeSummary(t *testing.T) {
	want := "Done. The current date is in /tmp/kero-probe-date.txt, as you asked; nothing else was touched."
	if got := Summary("claude", screen(t, "claude-done.txt")); got != want {
		t.Errorf("done: %q, want %q", got, want)
	}
	for _, name := range []string{"claude-idle.txt", "claude-working.txt", "claude-blocked.txt"} {
		if got := Summary("claude", screen(t, name)); got != "" {
			t.Errorf("%s: %q, want none", name, got)
		}
	}
	// A tool call that ended the screen leaves the words before it.
	s := strings.Replace(screen(t, "claude-done.txt"), "⏺ Done. The current date", "⏺ Read(x.go)\n  ⎿  Read 3 lines\n\n⏺ Bash(ls)\n  ⎿  a\n\n\n⏺ Gone. The current date", 1)
	s = strings.Replace(s, "\n\n⏺ Gone. The current date is in /tmp/kero-probe-date.txt, as you asked; nothing\n  else was touched.\n", "\n", 1)
	if got := Summary("claude", s); got != "I'll write the date to the probe file." {
		t.Errorf("after tool calls: %q", got)
	}
	long := strings.Replace(screen(t, "claude-done.txt"), "nothing\n  else was touched.", strings.Repeat("word ", 60), 1)
	if got := Summary("claude", long); len([]rune(got)) > summaryRunes || !strings.HasSuffix(got, "…") {
		t.Errorf("long: %d runes, %q", len([]rune(got)), got)
	}
	if got := Summary("codex", screen(t, "claude-done.txt")); got != "" {
		t.Errorf("codex: %q, want none (not known)", got)
	}
}

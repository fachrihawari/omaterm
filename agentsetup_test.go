package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/cli"
	"omaterm/internal/session"
)

// agentHome gives the test a home whose agents have no omaterm hook.
func agentHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
}

// An agent seen without its hook offers the integration once; Set Up
// installs the hooks, and the offer does not come back.
func TestAgentSetupOffer(t *testing.T) {
	agentHome(t)
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	a.offerAgentSetup(map[string]session.Info{"p": {ID: "p", Agent: "claude"}})
	tr.Frame()
	if !tr.HasText("Set Up") {
		t.Fatalf("no offer: %q", tr.Texts())
	}
	if err := tr.Click("Set Up"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !cli.HookInstalled("claude") {
		t.Fatal("Set Up did not install Claude Code's hook")
	}
	a.offerAgentSetup(map[string]session.Info{"p": {ID: "p", Agent: "codex"}})
	tr.Frame()
	if tr.HasText("Set Up") {
		t.Fatal("the offer came back once answered")
	}
}

// Not Now dismisses the offer for good; an agent whose hook is in, or
// that knows its conversation, offers nothing.
func TestAgentSetupNotNow(t *testing.T) {
	agentHome(t)
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	a.offerAgentSetup(map[string]session.Info{"p": {ID: "p", Agent: "opencode", AgentSession: nil}})
	tr.Frame()
	if err := tr.Click("Not Now"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if tr.HasText("Set Up") || !a.agentSetupAsked {
		t.Fatal("Not Now did not dismiss the offer")
	}

	b := newTestApp(t)
	defer stopTermPanes(b)
	b.offerAgentSetup(map[string]session.Info{
		"p": {ID: "p", Agent: "aider"},
		"q": {ID: "q", Agent: "claude", AgentSession: &agent.Session{Agent: "claude", ID: "s1"}},
	})
	if b.agentSetupOffer != "" {
		t.Fatalf("offered for %q", b.agentSetupOffer)
	}
}

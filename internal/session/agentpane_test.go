package session

import (
	"os/exec"
	"runtime"
	"testing"

	"omaterm/internal/agent"
)

// fakePane is a pane known only by its info.
type fakePane struct {
	Handle
	info Info
}

func (p *fakePane) ID() string { return p.info.ID }
func (p *fakePane) Info() Info { return p.info }

// fakeHost holds fake panes.
type fakeHost struct {
	Host
	panes []Handle
}

func (h fakeHost) Each(fn func(Handle)) {
	for _, p := range h.panes {
		fn(p)
	}
}

func claudePane(id, dir string) *fakePane {
	return &fakePane{info: Info{ID: id, Agent: "claude", Dir: dir}}
}

// A hook away from its pane is matched only to the one pane of its folder
// that runs the agent: never to one in another folder, nor a guess
// between two.
func TestFindAgentPaneGuessStaysInItsFolder(t *testing.T) {
	oterm, random := claudePane("a", "/p/oterm"), claudePane("b", "/p/random")
	h := fakeHost{panes: []Handle{random, oterm}}
	if p, away := FindAgentPane(h, 1, "claude", "", "/p/oterm"); p != Handle(oterm) || !away {
		t.Fatalf("got %v, away %v; want the oterm pane", p, away)
	}
	if p, _ := FindAgentPane(h, 1, "claude", "", "/p/elsewhere"); p != nil {
		t.Fatalf("picked %s in another folder", p.ID())
	}
	twice := fakeHost{panes: []Handle{oterm, claudePane("c", "/p/oterm")}}
	if p, _ := FindAgentPane(twice, 1, "claude", "", "/p/oterm"); p != nil {
		t.Fatalf("guessed %s between two", p.ID())
	}
	held := claudePane("d", "/p/oterm")
	held.info.AgentSession = &agent.Session{Agent: "claude", ID: "other"}
	if p, _ := FindAgentPane(fakeHost{panes: []Handle{held}}, 1, "claude", "", "/p/oterm"); p != nil {
		t.Fatal("took a pane that runs another conversation")
	}
}

// A conversation running away from its pane, as a Claude Code background
// session does, is found by the process in the pane that names it, as
// `claude attach <id>`.
func TestFindAgentPaneByConversation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("process lookups are Linux's and macOS's")
	}
	cmd := exec.Command("sh", "-c", "sleep 30; true", "8f0d0898")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	attached := &fakePane{info: Info{ID: "a", PID: cmd.Process.Pid, Dir: "/p/oterm"}}
	other := claudePane("b", "/p/random")
	h := fakeHost{panes: []Handle{other, attached}}
	p, away := FindAgentPane(h, 1, "claude", "8f0d0898-6f56-48f2-a0c5-ff1bb03ea35f", "/p/oterm")
	if p != Handle(attached) || !away {
		t.Fatalf("got %v, away %v; want the attached pane", p, away)
	}
}

package osc

import "testing"

// What the agent in a pane reports it is working on ends with its
// program's turn: the program's bare done makes it done, its word kept.
func TestAgentWorkEndsWithTheTurn(t *testing.T) {
	var rs Records
	rs.Apply(Record{ID: AgentID, State: "working", Msg: "Leading the release", Progress: 90, Updated: 1})
	rs.Apply(Record{ID: AgentID + "/tests", State: "working", Progress: -1, Updated: 1})
	rs.Apply(Record{ID: "other", State: "working", Progress: -1, Updated: 1})
	if !rs.AgentWorking() {
		t.Fatal("the agent's work is not seen")
	}
	rs.Apply(Record{State: "done", Progress: -1, Updated: 2})
	if rs.AgentWorking() {
		t.Fatal("the agent's work went on past the turn")
	}
	lead, _ := rs.Lead()
	if lead.ID != "other" || lead.State != "working" {
		// A program's own working report, with an id, is not the agent's.
		t.Fatalf("lead %+v", lead)
	}
	rs.Apply(Record{ID: "other", State: "done", Progress: -1, Updated: 3})
	if lead, _ := rs.Lead(); lead.ID != AgentID || lead.State != "done" || lead.Msg != "Leading the release" || lead.Progress != -1 {
		t.Fatalf("after the turn: %+v", lead)
	}
}

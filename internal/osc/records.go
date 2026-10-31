// Adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/osc.go.

package osc

import (
	"slices"
	"strings"
)

// MaxRecords is how many reports a pane keeps; past it, the one updated
// longest ago goes.
const MaxRecords = 64

// Records are a pane's status reports, by id. Its zero value is ready.
type Records struct {
	m map[string]Record
}

// Apply takes a report: a "clear" drops the id and those under it (all,
// without an id); any other replaces the report of its id.
func (rs *Records) Apply(r Record) {
	if rs.m == nil {
		rs.m = map[string]Record{}
	}
	if r.State == "clear" {
		if r.ID == "" {
			clear(rs.m)
			return
		}
		for id := range rs.m {
			if id == r.ID || strings.HasPrefix(id, r.ID+"/") {
				delete(rs.m, id)
			}
		}
		return
	}
	if r.ID == "" && (r.State == "done" || r.State == "idle") {
		// The program's turn ended: what the agent in it said it was
		// doing is over too, whether or not it said so.
		rs.EndTurn(r.Updated)
	}
	if _, ok := rs.m[r.ID]; !ok && len(rs.m) >= MaxRecords {
		oldest := ""
		first := true
		for id, o := range rs.m {
			if first || o.Updated < rs.m[oldest].Updated {
				oldest, first = id, false
			}
		}
		delete(rs.m, oldest)
	}
	rs.m[r.ID] = r
}

// Reset drops every report, as a full reset of the terminal does.
func (rs *Records) Reset() { clear(rs.m) }

// Len is how many reports there are.
func (rs *Records) Len() int { return len(rs.m) }

// DropLive drops the reports that end with their program: working,
// blocked and idle ones, as it exits or a new prompt shows. It reports
// whether any went.
func (rs *Records) DropLive() bool { return rs.drop("working", "blocked", "idle") }

// DismissFinished drops the done and error reports, once the user acted.
func (rs *Records) DismissFinished() bool { return rs.drop("done", "error") }

func (rs *Records) drop(states ...string) bool {
	changed := false
	for id, r := range rs.m {
		if slices.Contains(states, r.State) {
			delete(rs.m, id)
			changed = true
		}
	}
	return changed
}

// AgentID is the id of the reports the omaterm command makes from inside
// an agent's pane, apart from its program's, which have none.
const AgentID = "agent"

// EndTurn ends what the agent reported it was working on: its program's
// turn is over, so the work is done, and its last word stays as the
// pane's. It reports whether any report changed.
func (rs *Records) EndTurn(at int64) bool {
	changed := false
	for id, r := range rs.m {
		if r.State == "working" && (id == AgentID || strings.HasPrefix(id, AgentID+"/")) {
			r.State, r.Progress, r.Updated = "done", -1, at
			rs.m[id] = r
			changed = true
		}
	}
	return changed
}

// AgentWorking reports whether the agent, not its program, says it works.
func (rs *Records) AgentWorking() bool {
	for id, r := range rs.m {
		if r.State == "working" && (id == AgentID || strings.HasPrefix(id, AgentID+"/")) {
			return true
		}
	}
	return false
}

// Declared reports whether a program declared it works or waits, which
// guesses from the screen then leave alone.
func (rs *Records) Declared() bool {
	for _, r := range rs.m {
		if r.State == "working" || r.State == "blocked" {
			return true
		}
	}
	return false
}

// List returns the reports ordered by id, those without an app taking
// their nearest ancestor's.
func (rs *Records) List() []Record {
	recs := make([]Record, 0, len(rs.m))
	for _, r := range rs.m {
		recs = append(recs, r)
	}
	slices.SortFunc(recs, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
	apps := map[string]string{}
	for _, r := range recs {
		apps[r.ID] = r.App
	}
	for i := range recs {
		for id := recs[i].ID; recs[i].App == "" && id != ""; {
			j := strings.LastIndexByte(id, '/')
			if j < 0 {
				id = ""
			} else {
				id = id[:j]
			}
			recs[i].App = apps[id]
		}
	}
	return recs
}

// Lead is the report that tells the pane's state: blocked over working
// over error over done over idle, and of a state one that words it, as an
// agent's own over the bare one its program reports as its turn ends. ok
// is false with no reports.
func (rs *Records) Lead() (Record, bool) {
	recs := rs.List()
	for _, state := range []string{"blocked", "working", "error", "done", "idle"} {
		for _, worded := range []bool{true, false} {
			for _, r := range recs {
				if r.State == state && (r.Msg != "" || !worded) {
					return r, true
				}
			}
		}
	}
	return Record{}, false
}

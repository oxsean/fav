package coord

import (
	"encoding/json"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// A definition may be shared for use without its instructions: whoever runs it and may not read it sees its runs
// without them, in every answer, state, snapshot and event that carries a run, and its profile without how it is set up.

// hidesDef: run was dispatched with a definition p may not read (one gone since: unless p is an admin). The caller
// holds mu.
func (c *Coord) hidesDef(p Principal, run *task.Run) bool {
	if !c.team() {
		return false
	}
	d := c.agentDefs()[run.Agent]
	if d == nil {
		return run.Def != nil && !p.Admin
	}
	return !c.readsDef(p, d)
}

// cutDef takes out of run, a copy p is to see, what of its definition p may not read. The caller holds mu.
func (c *Coord) cutDef(p Principal, run *task.Run) {
	if !c.hidesDef(p, run) {
		return
	}
	switch s := run.Def; {
	case s != nil && s.From <= s.To && s.To <= len(run.Brief):
		run.Brief = run.Brief[:s.From] + run.Brief[s.To:]
	case run.Resume == "": // queued before runs said where the instructions are
		run.Brief = ""
	}
	run.Def = nil
	run.Profile = publicProfile(run.Profile)
}

// publicProfile is what anyone who may run an agent sees of how it runs: what the definition list shows them too.
func publicProfile(p tend.AgentProfile) tend.AgentProfile {
	return tend.AgentProfile{Name: p.Name, Provider: p.Provider, Model: p.Model, Effort: p.Effort, Machine: p.Machine}
}

// runViewFor is runView as p sees it. The caller holds mu.
func (c *Coord) runViewFor(p Principal) func(*task.State, string) any {
	return func(st *task.State, id string) any {
		r, _ := runView(st, id).(*task.Run)
		if r == nil {
			return nil
		}
		c.cutDef(p, r)
		return r
	}
}

// eventFor is e as p sees it: a run queued without what of its definition p may not read. The caller holds mu.
func (c *Coord) eventFor(p Principal, e journal.Event) journal.Event {
	if e.Type != task.ERunQueued {
		return e
	}
	var r task.Run
	if json.Unmarshal(e.Data, &r) != nil || !c.hidesDef(p, &r) {
		return e
	}
	c.cutDef(p, &r)
	e.Data, _ = json.Marshal(r)
	return e
}

// agentsFor are the agents p can pick, the definitions they may not read as publicProfile.
func (c *Coord) agentsFor(p Principal) []tend.AgentProfile {
	out := c.usableProfiles(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	ds := c.agentDefs()
	for i, a := range out {
		if d := ds[a.Name]; d != nil && c.team() && !c.readsDef(p, d) {
			out[i] = publicProfile(a)
		}
	}
	return out
}

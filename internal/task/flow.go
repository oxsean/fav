package task

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
)

// Workflow events.
const (
	ETaskStaged = "task_staged" // TaskStage: the task moves to a stage (the coordinator's rules, or a gate's decision)
	ETaskNoted  = "task_noted"  // TaskNote: a line for its workpad
)

// Stage gates and outputs.
const (
	GateHuman     = "human"   // the stage runs nothing: its approver passes it, a participant sends it back
	OutputVerdict = "verdict" // its run must end with tend run verdict
)

// Why a task in a workflow stands where it does.
const (
	WhyAdvance  = "advance"   // queued: its stage is done; the coordinator moves it on next
	WhyRework   = "rework"    // queued: its stage asked for changes; the coordinator sends it back next
	WhyMaxLoops = "max_loops" // waiting: sent back more often than its workflow allows
	WhyBlocked  = "blocked"   // waiting: its stage's run could not judge the work, or gave no verdict
	WhyBudget   = "budget"    // waiting: its runs spent what its workflow allows
)

// Note kinds.
const (
	NoteMessage = "message" // someone's message between stages
	NoteGate    = "gate"    // a gate's decision
	NoteRework  = "rework"  // why a stage sent it back
)

// Flow is a workflow frozen on a task when it was given one: later edits of the definition leave it be.
type Flow struct {
	Name     string  `json:"name"`
	Stages   []Stage `json:"stages"`
	MaxLoops int     `json:"max_loops,omitempty"`
	Budget   *Budget `json:"budget,omitempty"`
}

type Stage struct {
	Name     string `json:"name" yaml:"name"`
	Role     string `json:"role,omitempty" yaml:"role,omitempty"`           // planner | implement | review | test: picks the project's agent for it
	Agent    string `json:"agent,omitempty" yaml:"agent,omitempty"`         // an agent by name instead
	Gate     string `json:"gate,omitempty" yaml:"gate,omitempty"`           // human
	Output   string `json:"output,omitempty" yaml:"output,omitempty"`       // verdict
	OnRework string `json:"on_rework,omitempty" yaml:"on_rework,omitempty"` // where a rework goes; default the stage before
	Machine  string `json:"machine,omitempty" yaml:"machine,omitempty"`
	Check    bool   `json:"check,omitempty" yaml:"check,omitempty"`   // run the project's check hook after it; a failure is a rework
	Prompt   string `json:"prompt,omitempty" yaml:"prompt,omitempty"` // its brief template
}

// Budget bounds what a task's runs spend in all.
type Budget struct {
	USD     float64 `json:"usd,omitempty" yaml:"usd,omitempty"`
	Minutes int     `json:"minutes,omitempty" yaml:"minutes,omitempty"`
}

type TaskStage struct {
	ID    string `json:"id"`
	Stage string `json:"stage"`
	Loops int    `json:"loops"`
	Back  bool   `json:"back,omitempty"` // sent back, not moved on
}

type TaskNote struct {
	ID   string `json:"id"`
	Note Note   `json:"note"`
}

// Note is a line of a task's workpad that no run wrote: a message, a gate's decision, why a stage sent it back.
type Note struct {
	At    time.Time `json:"at,omitzero"`
	Stage string    `json:"stage,omitempty"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	By    string    `json:"by,omitempty"`
}

// maxNotes is how many notes a task keeps; older ones fall off.
const maxNotes = 200

// StageOf is the stage named name, nil when the flow has none.
func (f *Flow) StageOf(name string) *Stage {
	if f == nil {
		return nil
	}
	for i := range f.Stages {
		if f.Stages[i].Name == name {
			return &f.Stages[i]
		}
	}
	return nil
}

// Next is the stage after name, "" when name is the last.
func (f *Flow) Next(name string) string {
	i := slices.IndexFunc(f.Stages, func(s Stage) bool { return s.Name == name })
	if i < 0 || i+1 >= len(f.Stages) {
		return ""
	}
	return f.Stages[i+1].Name
}

// Back is where a rework of stage name goes: its on_rework, else the nearest stage before it that changes the work (runs
// something and judges nothing), else the nearest that runs something.
func (f *Flow) Back(name string) string {
	i := slices.IndexFunc(f.Stages, func(s Stage) bool { return s.Name == name })
	if i < 0 {
		return ""
	}
	if t := f.Stages[i].OnRework; t != "" && f.StageOf(t) != nil {
		return t
	}
	for _, works := range []func(Stage) bool{
		func(s Stage) bool { return s.Gate != GateHuman && s.Output != OutputVerdict },
		func(s Stage) bool { return s.Gate != GateHuman },
	} {
		for j := i - 1; j >= 0; j-- {
			if works(f.Stages[j]) {
				return f.Stages[j].Name
			}
		}
	}
	return f.Stages[0].Name
}

// Spent is what t's runs used: the CLIs' cost estimates, and the minutes of those that ended.
func (s *State) Spent(t *Task) (usd float64, minutes float64) {
	for _, r := range s.Runs {
		if r.Task != t.ID {
			continue
		}
		if r.Usage != nil {
			usd += r.Usage.CostUSD
		}
		if r.StartedAt != nil && r.EndedAt != nil {
			minutes += r.EndedAt.Sub(*r.StartedAt).Minutes()
		}
	}
	return usd, minutes
}

// overBudget: t's runs spent what its workflow allows.
func (s *State) overBudget(t *Task) bool {
	b := t.Flow.Budget
	if b == nil {
		return false
	}
	usd, min := s.Spent(t)
	return b.USD > 0 && usd >= b.USD || b.Minutes > 0 && min >= float64(b.Minutes)
}

// stageSituation is where started task t in a workflow stands once nothing else holds it: its stage's gate, or what
// the latest run of its stage came to.
func (s *State) stageSituation(t *Task) Situation {
	st := t.Flow.StageOf(t.Stage)
	if st == nil {
		return Situation{Kind: SitWaiting, Reason: WhyBlocked}
	}
	if st.Gate == GateHuman {
		return Situation{Kind: SitWaiting, Reason: WhyAccept}
	}
	since := max(t.StageSeq, t.StartSeq)
	last := s.Latest(t.ID)
	if last == nil || last.Seq <= since {
		if s.overBudget(t) {
			return Situation{Kind: SitWaiting, Reason: WhyBudget}
		}
		return Situation{Kind: SitQueued, Reason: WhyReady}
	}
	switch {
	case last.Waiting():
		return Situation{Kind: SitWaiting, Reason: last.Attention, Run: last.ID}
	case last.State == Exited && last.ExitCode != nil && *last.ExitCode == 0 && last.Attention == "":
		switch verdictOf(st, last) {
		case agent.VerdictPass:
			return Situation{Kind: SitQueued, Reason: WhyAdvance, Run: last.ID}
		case agent.VerdictRework:
			if t.Loops >= t.Flow.MaxLoops {
				return Situation{Kind: SitWaiting, Reason: WhyMaxLoops, Run: last.ID}
			}
			return Situation{Kind: SitQueued, Reason: WhyRework, Run: last.ID}
		}
		return Situation{Kind: SitWaiting, Reason: WhyBlocked, Run: last.ID}
	case last.Reason != "" && last.State != Exited:
		return Situation{Kind: SitWaiting, Reason: last.Reason, Run: last.ID}
	}
	return Situation{Kind: SitWaiting, Reason: last.State, Run: last.ID}
}

// verdictOf is what run r of stage st concluded: a failed check is a rework; a stage without a verdict passes when its
// run ends well; one that wants a verdict and got none is blocked.
func verdictOf(st *Stage, r *Run) string {
	if r.Checked != nil && r.Checked.Exit != 0 {
		return agent.VerdictRework
	}
	if st.Output != OutputVerdict {
		return agent.VerdictPass
	}
	if r.Verdict == nil {
		return agent.VerdictBlocked
	}
	return r.Verdict.Verdict
}

// Advancing are the tasks whose stage is done, for the coordinator to move on.
func (s *State) Advancing() []*Task { return s.situated(WhyAdvance) }

// Reworking are the tasks a stage sent back, for the coordinator to move back.
func (s *State) Reworking() []*Task { return s.situated(WhyRework) }

func (s *State) applyFlow(e journal.Event, seq int64, at time.Time) (bool, error) {
	switch e.Type {
	case ETaskStaged:
		var d TaskStage
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil || t.Flow.StageOf(d.Stage) == nil {
			return true, fmt.Errorf("no stage %s of task %s", d.Stage, d.ID)
		}
		t.Stage, t.Loops, t.StageSeq = d.Stage, d.Loops, seq
		t.Rev++
		t.UpdatedAt = at
	case ETaskNoted:
		var d TaskNote
		if err := json.Unmarshal(e.Data, &d); err != nil {
			return true, err
		}
		t := s.Tasks[d.ID]
		if t == nil {
			return true, fmt.Errorf("no task %s", d.ID)
		}
		d.Note.At = at
		t.Notes = append(t.Notes, d.Note)
		if len(t.Notes) > maxNotes {
			t.Notes = t.Notes[len(t.Notes)-maxNotes:]
		}
		t.Rev++
		t.UpdatedAt = at
	default:
		return false, nil
	}
	return true, nil
}

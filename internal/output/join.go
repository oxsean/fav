package output

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/oxsean/fav/internal/agent"
)

// Mark is a line of a run's marks.jsonl, at where output.log was when it was written (docs/design/runs/node.md).
// ⚠️ The agent can write the run directory: a mark only places things; who did what is the journal's.
type Mark struct {
	Type  string    `json:"type"`            // tend
	Event string    `json:"event"`           // start | turn | hook | exit | roll (the log went on in another file) | input | resolved | interrupt | diff
	ID    string    `json:"id,omitempty"`    // input: the message the agent took in; resolved: the request answered; interrupt: which
	N     int       `json:"n,omitempty"`     // turn: which; interrupt: the turn it ends
	Phase string    `json:"phase,omitempty"` // hook: begin | end; turn: end, where the line with its result ends
	Name  string    `json:"name,omitempty"`  // hook: which
	Code  *int      `json:"code,omitempty"`  // exit, hook end
	Files int       `json:"files,omitempty"` // diff: the files codex's diff of the turn (ID) changed
	Add   int       `json:"add,omitempty"`   // diff: lines added
	Del   int       `json:"del,omitempty"`   // diff: lines removed
	File  string    `json:"file"`
	Off   int64     `json:"off"`
	At    time.Time `json:"at"`
	Pos   int64     `json:"pos,omitempty"` // where the mark's line starts in marks.jsonl, as a reader found it
}

// Said is what the journal holds of a run's inputs, which Join fills events from.
type Said struct {
	Send      func(echo string) (agent.Send, bool) // the message the agent gave back with echo
	Answer    func(request string) agent.Answer    // the answer to request (zero: none, the agent dropped it)
	Interrupt func(id string) string               // who asked for interrupt id
}

// Join makes a stretch of a log's events whole: a user event the agent gave back a message with becomes you,
// with the message as the journal holds it (a message already in seen is not shown again), and the marks of that
// stretch, in any order, become events before the events at their offset. turn is the turn the stretch starts in.
func Join(evs []Event, marks []Mark, turn int, said Said, seen map[string]bool) []Event {
	marks = slices.Clone(marks)
	slices.SortStableFunc(marks, func(a, b Mark) int { return cmp.Compare(a.Off, b.Off) })
	out := make([]Event, 0, len(evs)+len(marks))
	add := func(m Mark) {
		e, ok := markEvent(m, said)
		if ok {
			e.Turn = turn
			out = append(out, e)
		}
	}
	for _, e := range evs {
		for len(marks) > 0 && !e.Temp && marks[0].Off <= e.Off {
			add(marks[0])
			marks = marks[1:]
		}
		if e.Echo != "" && said.Send != nil {
			if in, ok := said.Send(e.Echo); ok {
				if seen[in.ID] {
					continue
				}
				seen[in.ID] = true
				id, _ := json.Marshal(in.ID)
				e.Kind, e.Input, e.Text, e.By, e.Mode = KindYou, id, in.Text, in.By, in.Mode
				if e.At == "" && !in.At.IsZero() {
					e.At = in.At.Format(time.RFC3339)
				}
			}
		}
		if e.Turn > 0 {
			turn = e.Turn
		}
		out = append(out, e)
	}
	for _, m := range marks {
		add(m)
	}
	return out
}

// markEvent is the event a mark shows as; false for the marks that only place turns and files.
func markEvent(m Mark, said Said) (Event, bool) {
	e := Event{ID: m.File + ":" + strconv.FormatInt(m.Off, 10) + ":m" + strconv.FormatInt(m.Pos, 10), Off: m.Off, Stream: "tend"}
	if !m.At.IsZero() {
		e.At = m.At.Format(time.RFC3339)
	}
	switch m.Event {
	case "resolved":
		e.Kind, e.Request = KindResolved, m.ID
		if said.Answer != nil {
			a := said.Answer(m.ID).Settled()
			e.By, e.Decision = a.By, a.Decision
			if e.Decision == "" && a.Request != "" {
				e.Decision = map[bool]string{true: agent.DecisionAllow, false: agent.DecisionDeny}[a.Allow]
			}
		}
	case "interrupt":
		e.Kind, e.N = KindInterrupt, m.N
		if said.Interrupt != nil {
			e.By = said.Interrupt(m.ID)
		}
	case "hook":
		e.Kind, e.Mark, e.Name, e.Phase, e.Exit = KindMark, m.Event, m.Name, m.Phase, m.Code
	default:
		return Event{}, false
	}
	return e, true
}

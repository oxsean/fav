package coord

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// Answer is run.answer: the user's answer to a request the run waits on.
type Answer struct {
	Run string `json:"run"`
	agent.Answer
}

// SendMessage is run.send: a message for a running run, into its turn (Mode steer, the default), after it, or in
// place of the rest of it (agent.SendAfter, agent.SendInterrupt).
type SendMessage struct {
	Run  string `json:"run"`
	Text string `json:"text"`
	Mode string `json:"mode,omitempty"`
}

// Interrupt is run.interrupt: end turn Turn of a running run (0: the turn it is at); an ask for a turn that is over
// does nothing.
type Interrupt struct {
	Run  string `json:"run"`
	Turn int    `json:"turn,omitempty"`
}

const maxMessage = 64 << 10

func (c *Coord) runAnswer(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Answer
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	switch p.Decision {
	case "", agent.DecisionAllow, agent.DecisionAllowRun, agent.DecisionDeny:
		p.Answer = p.Answer.Settled() // a node that knows no decision reads allow
	default:
		return "", nil, bad("decision")
	}
	run, err := c.writableRun(who, p.Run)
	if err != nil {
		return "", nil, err
	}
	i := slices.IndexFunc(run.Requests, func(q agent.Request) bool { return q.ID == p.Request })
	if !task.Open(run.State) || i < 0 {
		return "", nil, requestGone("")
	}
	q := run.Requests[i]
	if q.Kind != agent.RequestQuestion && !c.canApprove(who, run) {
		return "", nil, forbidden("permission")
	}
	if j := slices.IndexFunc(run.Answers, func(a agent.Answer) bool { return a.Request == p.Request }); j >= 0 && !q.Failed {
		return "", nil, requestGone(run.Answers[j].By) // the first answer counts; one that did not reach the agent may be replaced
	}
	if p.Decision == agent.DecisionAllowRun && (!q.AllowRun || !run.CapsNow().AnswerScope) {
		return "", nil, bad("decision")
	}
	if p.Decision == agent.DecisionAllowRun && !c.nodeHas(run.Machine, node.FeatureAnswerScope) {
		return "", nil, &wire.Error{Code: wire.CodeProto, Detail: ReasonNodeOutdated}
	}
	if len(p.Message) > maxMessage {
		return "", nil, bad("message")
	}
	if q := run.Requests[i]; q.Kind == agent.RequestQuestion && p.Allow {
		for _, x := range q.Questions {
			if strings.TrimSpace(p.Answers[x.Question]) == "" {
				return "", nil, bad("answers")
			}
		}
	}
	p.Answer.By = who.User
	return run.ID, []journal.Event{journal.NewEvent(task.ERunAnswered, task.RunAnswer{ID: run.ID, Answer: p.Answer})}, nil
}

// requestGone: the request is answered (by whom, when known) or no longer asked.
func requestGone(by string) error { return &wire.Error{Code: wire.CodeRequestGone, Detail: by} }

// nodeHas: machine's node has feature, as far as is known (a node not connected is taken at its word once it is); the
// caller holds mu.
func (c *Coord) nodeHas(machine, feature string) bool {
	m := c.ms[machine]
	return m == nil || m.conn == nil || slices.Contains(m.hello.Features, feature)
}

func (c *Coord) runSend(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p SendMessage
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" || len(p.Text) > maxMessage {
		return "", nil, bad("text")
	}
	run, err := c.writableRun(who, p.Run)
	if err != nil {
		return "", nil, err
	}
	if !c.canUse(who, run.Machine, run.Project) {
		return "", nil, forbidden("machine " + run.Machine)
	}
	caps := run.CapsNow()
	switch p.Mode {
	case "", agent.SendSteer:
		p.Mode = ""
		if run.State != task.Running || !run.Stream || !caps.Steer {
			return "", nil, cannotSend("steer")
		}
	case agent.SendAfter:
		if run.State != task.Running || !caps.After {
			return "", nil, cannotSend(agent.SendAfter)
		}
	case agent.SendInterrupt:
		if run.State != task.Running || !caps.After || !caps.Interrupt || run.Turn < 1 {
			return "", nil, cannotSend(agent.SendInterrupt)
		}
		if !c.nodeHas(run.Machine, node.FeatureInterrupt) {
			return "", nil, &wire.Error{Code: wire.CodeProto, Detail: ReasonNodeOutdated}
		}
	default:
		return "", nil, bad("mode")
	}
	var b [6]byte
	rand.Read(b[:])
	m := agent.Send{ID: "m_" + hex.EncodeToString(b[:]), Text: p.Text, State: agent.SendQueued, Mode: p.Mode, By: who.User, At: time.Now()}
	events := []journal.Event{journal.NewEvent(task.ERunSent, task.RunSend{ID: run.ID, Send: m})}
	if p.Mode == agent.SendInterrupt {
		events = append(events, journal.NewEvent(task.ERunInterrupt, c.interruptOf(who, run, run.Turn)))
	}
	return run.ID, events, nil
}

// cannotSend: the run takes no message that way now.
func cannotSend(how string) error { return &wire.Error{Code: wire.CodeCannotSend, Detail: how} }

// interruptOf is who's ask to end turn of run: its node's id comes from the run and the turn, so asking again is the
// same ask.
func (c *Coord) interruptOf(who Principal, run *task.Run, turn int) task.RunInterrupt {
	return task.RunInterrupt{ID: run.ID, Turn: turn, Ask: "int_" + strconv.Itoa(turn), By: who.User}
}

func (c *Coord) runInterrupt(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Interrupt
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	run, err := c.writableRun(who, p.Run)
	if err != nil {
		return "", nil, err
	}
	turn := cmp.Or(p.Turn, run.Turn)
	switch {
	case run.State != task.Running || !run.CapsNow().Interrupt || turn < 1:
		return "", nil, conflict("cannot_interrupt")
	case turn < run.Turn:
		return "", nil, conflict("turn_over")
	case !c.nodeHas(run.Machine, node.FeatureInterrupt):
		return "", nil, &wire.Error{Code: wire.CodeProto, Detail: ReasonNodeOutdated}
	case run.Interrupt != nil && run.Interrupt.Turn == turn:
		return run.ID, nil, nil
	}
	return run.ID, []journal.Event{journal.NewEvent(task.ERunInterrupt, c.interruptOf(who, run, turn))}, nil
}

// input is an answer, a message or an ask to end a turn the node of a run has not taken yet.
type input struct {
	run       string
	answer    *agent.Answer
	send      *agent.Send
	interrupt *node.InterruptParams
}

// inputsFor are what machine m's running runs were given and its node has not taken; seen is its latest list. The
// caller holds mu.
func (c *Coord) inputsFor(m *machine, seen map[string]node.Snapshot) []input {
	var out []input
	due := func(key string) bool {
		if time.Since(c.sent[key]) < resendAfter {
			return false
		}
		c.sent[key] = time.Now()
		return true
	}
	for _, r := range c.st.Runs {
		s, onNode := seen[r.ID]
		if r.Machine != m.name || r.State != task.Running || !onNode {
			continue
		}
		for _, a := range r.Answers {
			if slices.ContainsFunc(s.Requests, func(q agent.Request) bool { return q.ID == a.Request }) && due(r.ID+"/"+a.Request) {
				out = append(out, input{run: r.ID, answer: &a})
			}
		}
		for _, x := range r.Sends {
			if x.State == agent.SendQueued && !x.Carried() && !slices.ContainsFunc(s.Sends, func(y agent.Send) bool { return y.ID == x.ID }) && due(r.ID+"/"+x.ID) {
				out = append(out, input{run: r.ID, send: &x})
			}
		}
		if i := r.Interrupt; i != nil && s.State.Turn <= i.Turn && slices.Contains(m.hello.Features, node.FeatureInterrupt) && due(r.ID+"/"+i.Ask) { // the node takes the same ask once
			out = append(out, input{run: r.ID, interrupt: &node.InterruptParams{Run: r.ID, Turn: i.Turn, ID: i.Ask}})
		}
	}
	return out
}

package coord

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
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

// SendMessage is run.send: a message for a running run.
type SendMessage struct {
	Run  string `json:"run"`
	Text string `json:"text"`
}

const maxMessage = 64 << 10

func (c *Coord) runAnswer(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Answer
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	run, err := c.writableRun(who, p.Run)
	if err != nil {
		return "", nil, err
	}
	i := slices.IndexFunc(run.Requests, func(q agent.Request) bool { return q.ID == p.Request })
	if !task.Open(run.State) || i < 0 {
		return "", nil, conflict("request_gone")
	}
	if run.Requests[i].Kind != agent.RequestQuestion && !c.canApprove(who, run) {
		return "", nil, forbidden("permission")
	}
	if slices.ContainsFunc(run.Answers, func(a agent.Answer) bool { return a.Request == p.Request }) {
		return "", nil, conflict("answered")
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
	return run.ID, []journal.Event{journal.NewEvent(task.ERunAnswered, task.RunAnswer{ID: run.ID, Answer: p.Answer})}, nil
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
	if run.State != task.Running || !run.Stream {
		return "", nil, conflict("cannot_send")
	}
	var b [6]byte
	rand.Read(b[:])
	m := agent.Send{ID: "m_" + hex.EncodeToString(b[:]), Text: p.Text, State: agent.SendQueued, At: time.Now()}
	return run.ID, []journal.Event{journal.NewEvent(task.ERunSent, task.RunSend{ID: run.ID, Send: m})}, nil
}

// input is an answer or a message the node of a run has not taken yet.
type input struct {
	run    string
	answer *agent.Answer
	send   *agent.Send
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
			if x.State == agent.SendQueued && !slices.ContainsFunc(s.Sends, func(y agent.Send) bool { return y.ID == x.ID }) && due(r.ID+"/"+x.ID) {
				out = append(out, input{run: r.ID, send: &x})
			}
		}
	}
	return out
}

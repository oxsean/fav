package node

import (
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// A stream run talks to its agent both ways in claude's stream-json: the brief goes in as the first message;
// permission prompts and questions come out as control requests that wait in the run's state until someone answers
// (answers.jsonl); messages for the agent while it runs wait in inbox.jsonl until they are written to it.
const (
	answersFile = "answers.jsonl"
	inboxFile   = "inbox.jsonl"

	streamEvery    = 250 * time.Millisecond
	turnGrace      = 2 * time.Second // after a turn ends: no new output this long closes stdin, and the agent exits
	interruptGrace = 3 * time.Second // a stop interrupts the turn first; the tree is stopped after this
	maxSends       = 50
	maxSendText    = 1 << 10
	maxSummary     = 500
	maxAnswerText  = 64 << 10
	maxSendIn      = 64 << 10
)

// streamIn is the agent's stdin.
type streamIn struct {
	mu     sync.Mutex
	w      *os.File
	closed bool
}

var errClosed = errors.New("the agent's input is closed")

func (in *streamIn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return errClosed
	}
	_, err = in.w.Write(append(b, '\n'))
	return err
}

func (in *streamIn) close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.closed {
		in.closed = true
		in.w.Close()
	}
}

func (in *streamIn) isClosed() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.closed
}

func userMessage(text string) any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}
}

func controlRequest(id string, req map[string]any) any {
	return map[string]any{"type": "control_request", "request_id": id, "request": req}
}

func controlResponse(id string, resp any) any {
	return map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": resp}}
}

func controlError(id, msg string) any {
	return map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": msg}}
}

// proto is how a stream run's agent speaks on stdin and stdout.
type proto interface {
	begin(brief string)                     // send the brief
	line(text []byte) bool                  // take a line of output it knows; false leaves it to the common reading
	answer(p pending, a agent.Answer) error // answer a request
	message(text string) error              // a message while it runs
	interrupt()                             // end the current turn
}

// startStream sends the agent its brief.
func (s *sup) startStream() {
	brief, err := os.ReadFile(filepath.Join(s.dir, "prompt.md"))
	if err != nil {
		s.in.close()
		return
	}
	go s.proto.begin(string(brief)) // ⚠️ a brief larger than the pipe waits for the agent to read it
}

// request records what the agent now waits on, and how to answer it.
func (s *sup) request(req agent.Request, p pending) {
	s.mu.Lock()
	s.inputs[req.ID] = p
	s.out.turnDone = time.Time{}
	s.mu.Unlock()
	s.keep(func(st *State) {
		st.Requests = append(st.Requests, req)
		s.markRequests(st)
	})
}

// pending is what answering a request needs.
type pending struct {
	tool   string
	input  json.RawMessage   // claude: the tool's input
	id     json.RawMessage   // codex: the JSON-RPC id to answer
	method string            // codex: what it asked
	qids   map[string]string // codex: question → its id
}

// markRequests sets the attention from the waiting requests: a question first, else a permission; with none it clears
// what it set. The caller holds mu.
func (s *sup) markRequests(st *State) {
	if len(st.Requests) == 0 {
		if s.reqAttention {
			st.Attention, st.Ask, s.reqAttention = "", "", false
		}
		return
	}
	st.Attention, st.Ask, s.reqAttention = AttentionPermission, "", true
	for _, r := range st.Requests {
		if r.Kind == agent.RequestQuestion && len(r.Questions) > 0 {
			st.Attention, st.Ask = AttentionAsked, r.Questions[0].Question
			return
		}
	}
}

func questionsOf(input json.RawMessage) []agent.Question {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
			Multi    bool   `json:"multiSelect"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	json.Unmarshal(input, &in)
	var out []agent.Question
	for _, q := range in.Questions {
		aq := agent.Question{Question: q.Question, Header: q.Header, Multi: q.Multi}
		for _, o := range q.Options {
			aq.Options = append(aq.Options, o.Label)
		}
		out = append(out, aq)
	}
	return out
}

// summaryOf is what a tool would do, from the input fields that say it best.
func summaryOf(input json.RawMessage, description string) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	for _, k := range []string{"command", "file_path", "notebook_path", "path", "url", "pattern", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			return clip(v, maxSummary)
		}
	}
	if description != "" {
		return clip(description, maxSummary)
	}
	return clip(string(input), maxSummary)
}

// takeAnswers writes the answers that came since the last look to the agent.
func (s *sup) takeAnswers() {
	as, next := linesFrom(filepath.Join(s.dir, answersFile), s.answersAt, func(a agent.Answer) bool { return a.Request != "" })
	s.answersAt = next
	for _, a := range as {
		s.mu.Lock()
		p, ok := s.inputs[a.Request]
		delete(s.inputs, a.Request)
		s.mu.Unlock()
		if !ok {
			continue
		}
		if !a.Allow {
			s.mu.Lock()
			s.userDenied[p.tool] = true
			s.mu.Unlock()
		}
		s.proto.answer(p, a)
		s.mu.Lock()
		s.seen = time.Now() // it waited on the user, not stalled
		s.mu.Unlock()
		s.keep(func(st *State) {
			st.Requests = slices.DeleteFunc(slices.Clone(st.Requests), func(r agent.Request) bool { return r.ID == a.Request })
			s.markRequests(st)
		})
	}
}

// takeInbox writes the messages that came since the last look to the agent; once its input is closed they fail.
func (s *sup) takeInbox() {
	ms, next := linesFrom(filepath.Join(s.dir, inboxFile), s.inboxAt, func(m agent.Send) bool { return m.ID != "" && m.Text != "" })
	s.inboxAt = next
	if len(ms) == 0 {
		return
	}
	for i := range ms {
		ms[i].State = agent.SendFailed
		if s.proto.message(ms[i].Text) == nil {
			ms[i].State = agent.SendSent
			s.mu.Lock()
			s.out.turnDone, s.seen, s.sent = time.Time{}, time.Now(), true // it starts a turn, or joins the one running
			s.mu.Unlock()
		}
		ms[i].Text = clip(ms[i].Text, maxSendText)
	}
	s.keep(func(st *State) { st.Sends = keepSends(append(slices.Clone(st.Sends), ms...)) })
}

func keepSends(ss []agent.Send) []agent.Send {
	if len(ss) > maxSends {
		ss = ss[len(ss)-maxSends:]
	}
	return ss
}

// streamTick answers, sends, and closes the agent's input once a turn has ended and nothing more came.
func (s *sup) streamTick() {
	s.takeAnswers()
	s.takeInbox()
	s.mu.Lock()
	done, sent := s.out.turnDone, s.sent
	s.mu.Unlock()
	if !done.IsZero() && (!sent || time.Since(done) > turnGrace) { // only a message it was sent can start another turn
		s.in.close()
	}
}

// endStream closes the agent's input for good: messages still waiting fail, requests no one answered go.
func (s *sup) endStream() {
	s.in.close()
	s.takeInbox()
}

// settleRequests clears what an ended run still waited on. The caller holds mu.
func (s *sup) settleRequests(st *State) {
	st.Requests = nil
	if s.reqAttention {
		st.Attention, st.Ask, s.reqAttention = "", "", false
	}
}

// AnswerParams answers a request run Run waits on.
type AnswerParams struct {
	Run    string       `json:"run"`
	Answer agent.Answer `json:"answer"`
}

// SendParams gives run Run a message while it runs.
type SendParams struct {
	Run  string     `json:"run"`
	Send agent.Send `json:"send"`
}

// Answer passes an answer to the run's supervisor. Answering again is a no-op; a request the run no longer waits on
// is a conflict.
func (n *Node) Answer(p AnswerParams) (Snapshot, error) {
	if !runID.MatchString(p.Run) || p.Answer.Request == "" || len(p.Answer.Message) > maxAnswerText {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "answer"}
	}
	s, err := n.Snapshot(p.Run)
	if err != nil {
		return Snapshot{}, err
	}
	path := filepath.Join(n.runDir(p.Run), answersFile)
	done, _ := linesFrom(path, 0, func(a agent.Answer) bool { return a.Request == p.Answer.Request })
	if len(done) > 0 {
		return s, nil
	}
	if s.State.State != StateRunning || !slices.ContainsFunc(s.Requests, func(r agent.Request) bool { return r.ID == p.Answer.Request }) {
		return Snapshot{}, &wire.Error{Code: wire.CodeConflict, Detail: "request_gone"}
	}
	if err := appendLine(path, p.Answer); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// Send queues a message for a running stream run; sending the same id again is a no-op.
func (n *Node) Send(p SendParams) (Snapshot, error) {
	p.Send.Text = strings.TrimSpace(p.Send.Text)
	if !runID.MatchString(p.Run) || p.Send.ID == "" || p.Send.Text == "" || len(p.Send.Text) > maxSendIn {
		return Snapshot{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "send"}
	}
	dir := n.runDir(p.Run)
	var spec Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
		if !paths.Exists(dir) || errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Run}
		}
		return Snapshot{}, err
	}
	s, err := n.Snapshot(p.Run)
	if err != nil {
		return Snapshot{}, err
	}
	if slices.ContainsFunc(s.Sends, func(m agent.Send) bool { return m.ID == p.Send.ID }) {
		return s, nil
	}
	if !spec.Stream || s.State.State != StateRunning && s.State.State != StateStarting {
		return Snapshot{}, &wire.Error{Code: wire.CodeConflict, Detail: "cannot_send"}
	}
	p.Send.State, p.Send.At = agent.SendQueued, cmp.Or(p.Send.At, time.Now())
	if err := appendLine(filepath.Join(dir, inboxFile), p.Send); err != nil {
		return Snapshot{}, err
	}
	return n.Snapshot(p.Run)
}

// withQueued adds the messages the supervisor has not taken yet to st's; a run that ended never takes them.
func withQueued(dir string, st State) []agent.Send {
	queued, _ := linesFrom(filepath.Join(dir, inboxFile), 0, func(m agent.Send) bool { return m.ID != "" })
	out := slices.Clone(st.Sends)
	for _, m := range queued {
		if slices.ContainsFunc(out, func(x agent.Send) bool { return x.ID == m.ID }) {
			continue
		}
		m.State, m.Text = agent.SendQueued, clip(m.Text, maxSendText)
		if Terminal(st.State) || st.State == StateUnknown {
			m.State = agent.SendFailed
		}
		out = append(out, m)
	}
	return keepSends(out)
}

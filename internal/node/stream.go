package node

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/wire"
)

// A stream run talks to its agent both ways in claude's stream-json: the brief goes in as the first message;
// permission prompts and questions come out as control requests that wait in the run's state until someone answers
// (answers.jsonl); messages for the agent while it runs wait in inbox.jsonl until they are written to it.
const (
	answersFile    = "answers.jsonl"
	inboxFile      = "inbox.jsonl"
	interruptsFile = "interrupts.jsonl" // turns to interrupt (InterruptParams)
	briefInput     = "brief"            // the id the brief goes to the agent with

	streamEvery    = 250 * time.Millisecond
	turnGrace      = 2 * time.Second  // after a turn ends: no new output this long closes stdin, and the agent exits
	seenWait       = 30 * time.Second // a message sent and not yet taken in keeps stdin open this long after a turn ends
	interruptGrace = 3 * time.Second  // a stop interrupts the turn first; the tree is stopped after this
	maxSends       = 50
	maxSendText    = 1 << 10
	maxSummary     = 500
	maxAnswerText  = 64 << 10
	maxSendIn      = 64 << 10
)

// streamIn is the agent's stdin. One goroutine writes it, in the order things were sent: whoever sends never waits
// for the agent to read, since the agent may itself wait for its output to be read.
type streamIn struct {
	mu     sync.Mutex
	cond   sync.Cond
	w      *os.File
	queue  []*inItem
	queued int     // bytes in queue
	flight *inItem // being written
	closed bool    // takes nothing more
	told   int     // items with a done not called yet
	ended  chan struct{}
}

type inItem struct {
	b       []byte
	done    func(error) // told once whether b reached the agent's input
	settled bool
}

var (
	errClosed    = errors.New("the agent's input is closed")
	errInputFull = errors.New("too much waits for the agent's input")
)

func newStreamIn(w *os.File) *streamIn {
	in := &streamIn{w: w, ended: make(chan struct{})}
	in.cond.L = &in.mu
	go in.write()
	return in
}

func (in *streamIn) send(v any) error { return in.sendThen(v, nil) }

// sendThen queues v for the agent's input; done, unless nil, is told once it was written or could not be. An error
// means it was not queued, and done is not told.
func (in *streamIn) sendThen(v any, done func(error)) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	switch {
	case in.closed:
		return errClosed
	case in.queued > 0 && in.queued+len(b) >= maxQueuedIn:
		return errInputFull
	}
	it := &inItem{b: append(b, '\n'), done: done}
	in.queue, in.queued = append(in.queue, it), in.queued+len(it.b)
	if done != nil {
		in.told++
	}
	in.cond.Signal()
	return nil
}

func (in *streamIn) write() {
	defer close(in.ended)
	for {
		in.mu.Lock()
		for len(in.queue) == 0 && !in.closed {
			in.cond.Wait()
		}
		if len(in.queue) == 0 {
			in.mu.Unlock()
			in.w.Close()
			return
		}
		it := in.queue[0]
		in.queue, in.queued, in.flight = in.queue[1:], in.queued-len(it.b), it
		in.mu.Unlock()
		_, err := in.w.Write(it.b)
		in.settle([]*inItem{it}, err)
		if err != nil { // the agent is gone
			in.mu.Lock()
			rest := in.queue
			in.queue, in.queued, in.closed = nil, 0, true
			in.mu.Unlock()
			in.w.Close()
			in.settle(rest, err)
			return
		}
	}
}

// settle tells items' done what became of them, once each.
func (in *streamIn) settle(items []*inItem, err error) {
	var tell []*inItem
	in.mu.Lock()
	for _, it := range items {
		if it == in.flight {
			in.flight = nil
		}
		if !it.settled {
			it.settled = true
			if it.done != nil {
				in.told--
				tell = append(tell, it)
			}
		}
	}
	in.mu.Unlock()
	for _, it := range tell {
		it.done(err)
	}
}

// close takes nothing more; what is queued is still written, then the input closes.
func (in *streamIn) close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.closed = true
	in.cond.Broadcast()
}

func (in *streamIn) isClosed() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.closed
}

// waiting: something sent with a done has not reached the agent's input yet.
func (in *streamIn) waiting() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.told > 0
}

// finish closes the input and waits up to d for what is queued; what is still not written then (a child the agent
// left holds its input and does not read) is told it failed.
func (in *streamIn) finish(d time.Duration) {
	in.close()
	select {
	case <-in.ended:
		return
	case <-time.After(d):
	}
	in.mu.Lock()
	left := in.queue
	if in.flight != nil {
		left = append([]*inItem{in.flight}, left...)
	}
	in.queue, in.queued = nil, 0
	in.mu.Unlock()
	in.settle(left, errClosed)
}

// userMessage is claude's user message; it gives uuid back when it takes the message in (--replay-user-messages).
func userMessage(text, uuid string) any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}, "uuid": uuid}
}

// uuidFor is the uuid message id of run goes to claude with: the same id always gets the same one.
func uuidFor(run, id string) string {
	h := sha256.Sum256([]byte(run + "/" + id))
	h[6], h[8] = h[6]&0x0f|0x50, h[8]&0x3f|0x80
	x := hex.EncodeToString(h[:16])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
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

// ownReport tells a command that runs nothing but this tend's own run reports.
func ownReport(command string) bool {
	self, err := os.Executable()
	return err == nil && agent.OwnReport(command, self)
}

// proto is how a stream run's agent speaks on stdin and stdout.
type proto interface {
	begin(brief string)                                       // send the brief
	line(text []byte, at logPos) bool                         // take a line of output it knows, logged at at; false leaves it to the common reading
	answer(p pending, a agent.Answer, done func(error)) error // answer a request; done is told once it reached the agent
	message(id, text string, done func(error)) error          // a message while it runs; done is told once it reached the agent
	interrupt(id string)                                      // end the current turn
}

// startStream sends the agent its brief.
func (s *sup) startStream() {
	brief, err := os.ReadFile(filepath.Join(s.dir, "prompt.md"))
	if err != nil {
		s.in.close()
		return
	}
	s.proto.begin(string(brief))
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
		s.proto.answer(p, a, nil)
		s.mu.Lock()
		s.seen = time.Now() // it waited on the user, not stalled
		s.mu.Unlock()
		s.keep(func(st *State) {
			st.Requests = slices.DeleteFunc(slices.Clone(st.Requests), func(r agent.Request) bool { return r.ID == a.Request })
			s.markRequests(st)
		})
	}
}

// takeInbox queues the messages that came since the last look for the agent; each is sent once it reached the
// agent's input (delivered), and fails when that input is closed.
func (s *sup) takeInbox() {
	ms, next := linesFrom(filepath.Join(s.dir, inboxFile), s.inboxAt, func(m agent.Send) bool { return m.ID != "" && m.Text != "" })
	s.inboxAt = next
	if len(ms) == 0 {
		return
	}
	texts := make([]string, len(ms))
	for i := range ms {
		texts[i], ms[i].State, ms[i].Text = ms[i].Text, agent.SendQueued, clip(ms[i].Text, maxSendText)
	}
	s.keep(func(st *State) { st.Sends = keepSends(append(slices.Clone(st.Sends), ms...)) })
	for i, m := range ms {
		if err := s.proto.message(m.ID, texts[i], func(err error) { s.delivered(m.ID, err) }); err != nil {
			s.delivered(m.ID, err)
		}
	}
}

// delivered records whether message id reached the agent's input.
func (s *sup) delivered(id string, err error) {
	state := agent.SendFailed
	if err == nil {
		state = agent.SendSent
		s.mu.Lock()
		s.out.turnDone, s.seen, s.sent = time.Time{}, time.Now(), true // it starts a turn, or joins the one running
		s.mu.Unlock()
	}
	s.keep(func(st *State) {
		st.Sends = slices.Clone(st.Sends)
		for i := range st.Sends {
			if st.Sends[i].ID == id && st.Sends[i].State != agent.SendSeen { // it may have come back already
				st.Sends[i].State = state
			}
		}
	})
}

// sendOfUUID is the message sent to claude with uuid, "" when none was.
func (s *sup) sendOfUUID(uuid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.st.Sends {
		if uuidFor(s.spec.Run, m.ID) == uuid {
			return m.ID
		}
	}
	return ""
}

// tookIn records that the agent took message id in, its output giving it back at at; only the first time counts:
// claude gives several messages sent together back once each, the last one holding all their text.
func (s *sup) tookIn(id string, at logPos) {
	s.mu.Lock()
	i := slices.IndexFunc(s.st.Sends, func(m agent.Send) bool { return m.ID == id })
	fresh := i >= 0 && s.st.Sends[i].State != agent.SendSeen
	s.mu.Unlock()
	if !fresh {
		return
	}
	s.markAt(Mark{Event: markInput, ID: id}, at)
	s.keep(func(st *State) {
		st.Sends = slices.Clone(st.Sends)
		for i := range st.Sends {
			if st.Sends[i].ID == id {
				st.Sends[i].State = agent.SendSeen
			}
		}
	})
}

// unseen: a message reached the agent's input and it has not taken it in yet.
func (s *sup) unseen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.st.Sends, func(m agent.Send) bool { return m.State == agent.SendSent })
}

// resolved marks where request id was answered, at at; one the agent dropped by itself no longer waits.
func (s *sup) resolved(id string, at logPos) {
	s.markAt(Mark{Event: markResolved, ID: id}, at)
	s.mu.Lock()
	_, waits := s.inputs[id]
	delete(s.inputs, id)
	s.mu.Unlock()
	if waits {
		s.keep(func(st *State) {
			st.Requests = slices.DeleteFunc(slices.Clone(st.Requests), func(r agent.Request) bool { return r.ID == id })
			s.markRequests(st)
		})
	}
}

func (s *sup) markAt(m Mark, at logPos) {
	if s.log != nil {
		s.log.markAt(m, at)
	}
}

// interruptAsk is a line of interrupts.jsonl.
type interruptAsk struct {
	ID   string    `json:"id"`
	Turn int       `json:"turn"`
	At   time.Time `json:"at"`
}

// takeInterrupts ends the current turn when one that came since the last look asks for it, and closes the agent's
// input so it exits after; an ask for a turn that is over does nothing. It answers whether it interrupted.
func (s *sup) takeInterrupts() bool {
	asks, next := linesFrom(filepath.Join(s.dir, interruptsFile), s.interruptsAt, func(a interruptAsk) bool { return a.ID != "" })
	s.interruptsAt = next
	for _, a := range asks {
		var now output.State
		if s.log != nil {
			now = s.log.turn()
		}
		if max(now.Turn, 1) != a.Turn || now.Closed || s.in.isClosed() {
			continue
		}
		s.proto.interrupt(a.ID)
		s.markAt(Mark{Event: markInterrupt, ID: a.ID, N: a.Turn}, logPos{})
		s.in.close()
		return true
	}
	return false
}

func keepSends(ss []agent.Send) []agent.Send {
	if len(ss) > maxSends {
		ss = ss[len(ss)-maxSends:]
	}
	return ss
}

// streamTick answers, sends, interrupts, and closes the agent's input once a turn has ended and nothing more came. It
// answers whether it interrupted the turn.
func (s *sup) streamTick() bool {
	s.takeAnswers()
	s.takeInbox()
	if s.takeInterrupts() {
		return true
	}
	s.mu.Lock()
	done, sent := s.out.turnDone, s.sent
	s.mu.Unlock()
	// only a message it was sent can start another turn, one on its way may be about to, and one it has not taken in
	// yet will (claude takes a message sent while it only writes after that turn's result)
	if !done.IsZero() && (!sent || time.Since(done) > turnGrace) && !s.in.waiting() && (!s.unseen() || time.Since(done) > seenWait) {
		s.in.close()
	}
	return false
}

// endStream closes the agent's input for good: messages still waiting fail, requests no one answered go.
func (s *sup) endStream() {
	s.in.finish(drainWait)
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

// Features of stream runs: FeatureInputMarks, marks.jsonl places where the agent took a message in (input) and
// where a request was answered (resolved), and a message the agent took in is seen; FeatureInterrupt, run.interrupt.
const (
	FeatureInputMarks = "input_marks"
	FeatureInterrupt  = "interrupt"
)

// InterruptParams ends turn Turn of stream run Run, unless that turn is over by the time the run's supervisor reads it.
type InterruptParams struct {
	Run  string `json:"run"`
	Turn int    `json:"turn"`
	ID   string `json:"id,omitempty"` // the same id again is a no-op; "" gets a new one
}

// Interrupted answers run.interrupt.
type Interrupted struct {
	ID string `json:"id"`
	Snapshot
}

var markID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// Interrupt asks the supervisor of a running stream run to interrupt a turn. An ended run's turns are all over: it
// changes nothing.
func (n *Node) Interrupt(p InterruptParams) (Interrupted, error) {
	if !runID.MatchString(p.Run) || p.Turn < 1 || p.ID != "" && !markID.MatchString(p.ID) {
		return Interrupted{}, &wire.Error{Code: wire.CodeBadRequest, Detail: "interrupt"}
	}
	dir := n.runDir(p.Run)
	var spec Spec
	if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
		if !paths.Exists(dir) || errors.Is(err, os.ErrNotExist) {
			return Interrupted{}, &wire.Error{Code: wire.CodeNotFound, Detail: p.Run}
		}
		return Interrupted{}, err
	}
	s, err := n.Snapshot(p.Run)
	if err != nil {
		return Interrupted{}, err
	}
	path := filepath.Join(dir, interruptsFile)
	if p.ID == "" {
		p.ID = newMarkID("int_")
	} else if done, _ := linesFrom(path, 0, func(a interruptAsk) bool { return a.ID == p.ID }); len(done) > 0 {
		return Interrupted{ID: p.ID, Snapshot: s}, nil
	}
	if Terminal(s.State.State) {
		return Interrupted{ID: p.ID, Snapshot: s}, nil
	}
	if !spec.Stream {
		return Interrupted{}, &wire.Error{Code: wire.CodeConflict, Detail: "cannot_interrupt"}
	}
	if err := appendLine(path, interruptAsk{ID: p.ID, Turn: p.Turn, At: time.Now()}); err != nil {
		return Interrupted{}, err
	}
	return Interrupted{ID: p.ID, Snapshot: s}, nil
}

func newMarkID(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
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

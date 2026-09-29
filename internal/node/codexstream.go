package node

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
)

// codexProto is `codex app-server`: JSON-RPC 2.0, one message per line. tend starts a thread (or resumes the run's
// session) and a turn with the brief; approvals and questions come as server requests; a message while a turn runs
// steers it, between turns it starts the next one.
type codexProto struct {
	s      *sup
	mu     sync.Mutex
	next   int
	calls  map[int]codexCall // what each request tend sent was
	thread string
	turn   string // the turn in progress, "" between turns
	brief  string
}

type codexCall struct {
	method string
	text   string // turn/steer: the message, sent as a new turn when the steer comes too late
	input  string // turn/steer: the message's id
}

var errNotReady = errors.New("the agent has no thread yet")

func (c *codexProto) send(v map[string]any) error { return c.sendThen(v, nil) }

func (c *codexProto) sendThen(v map[string]any, done func(error)) error {
	v["jsonrpc"] = "2.0"
	return c.s.in.sendThen(v, done)
}

func (c *codexProto) call(method string, params any) error {
	return c.callThen(method, params, codexCall{}, nil)
}

func (c *codexProto) callThen(method string, params any, call codexCall, done func(error)) error {
	c.mu.Lock()
	c.next++
	id := c.next
	if c.calls == nil {
		c.calls = map[int]codexCall{}
	}
	call.method = method
	c.calls[id] = call
	c.mu.Unlock()
	return c.sendThen(map[string]any{"id": id, "method": method, "params": params}, done)
}

func (c *codexProto) reply(id json.RawMessage, result any, done func(error)) error {
	return c.sendThen(map[string]any{"id": id, "result": result}, done)
}

func textInput(text string) []any { return []any{map[string]any{"type": "text", "text": text}} }

// turnInput is a turn's input: text, and the id its userMessage item gives back (clientId).
func turnInput(thread, text, id string) map[string]any {
	return map[string]any{"threadId": thread, "input": textInput(text), "clientUserMessageId": id}
}

func (c *codexProto) begin(brief string) {
	c.mu.Lock()
	c.brief = brief
	c.mu.Unlock()
	c.call("initialize", map[string]any{"clientInfo": map[string]any{"name": "tend", "version": "1"}})
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *codexProto) line(text []byte, at logPos) bool {
	var m rpcMessage
	if json.Unmarshal(text, &m) != nil || m.Method == "" && len(m.ID) == 0 {
		return false
	}
	switch {
	case m.Method == "":
		c.response(m)
	case len(m.ID) > 0:
		c.request(m)
	default:
		c.notification(m, at)
	}
	return true
}

// response takes the answer to a request tend sent.
func (c *codexProto) response(m rpcMessage) {
	id, _ := strconv.Atoi(string(m.ID))
	c.mu.Lock()
	call := c.calls[id]
	delete(c.calls, id)
	thread, brief := c.thread, c.brief
	c.mu.Unlock()
	if m.Error != nil {
		switch call.method {
		case "turn/steer": // the turn ended meanwhile: the message starts the next one
			if thread != "" {
				c.call("turn/start", turnInput(thread, call.text, call.input))
			}
		case "initialize", "thread/start", "thread/resume", "turn/start":
			c.s.mu.Lock()
			c.s.out.err = clip(m.Error.Message, maxFinal)
			c.s.mu.Unlock()
			c.s.in.close() // it cannot go on: the agent ends and the run records why
		}
		return
	}
	switch call.method {
	case "initialize":
		c.send(map[string]any{"method": "initialized"})
		if c.s.spec.Session != "" {
			c.call("thread/resume", map[string]any{"threadId": c.s.spec.Session, "approvalPolicy": "on-request"})
		} else {
			c.call("thread/start", map[string]any{"cwd": c.s.spec.Dir, "approvalPolicy": "on-request"})
		}
	case "thread/start", "thread/resume":
		var r struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
			Sandbox map[string]any `json:"sandbox"`
		}
		json.Unmarshal(m.Result, &r)
		c.mu.Lock()
		c.thread = r.Thread.ID
		c.mu.Unlock()
		if r.Thread.ID != "" && !c.s.bound() {
			c.s.keep(func(st *State) { st.Session = r.Thread.ID })
		}
		turn := turnInput(r.Thread.ID, brief, briefInput)
		if r.Sandbox["type"] == "workspaceWrite" { // tend run note / ask / verdict write the run's directory
			roots, _ := r.Sandbox["writableRoots"].([]any)
			r.Sandbox["writableRoots"] = append(roots, c.s.dir)
			turn["sandboxPolicy"] = r.Sandbox
		}
		c.call("turn/start", turn)
	case "turn/start":
		var r struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		json.Unmarshal(m.Result, &r)
		c.mu.Lock()
		if r.Turn.Status == "inProgress" || r.Turn.Status == "" {
			c.turn = r.Turn.ID
		}
		c.mu.Unlock()
	}
}

// request takes a server request: an approval or questions wait for the user; anything else is refused.
func (c *codexProto) request(m rpcMessage) {
	var p struct {
		Command     string          `json:"command"`
		Reason      string          `json:"reason"`
		GrantRoot   string          `json:"grantRoot"`
		Permissions json.RawMessage `json:"permissions"`
		Questions   []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	json.Unmarshal(m.Params, &p)
	req := agent.Request{ID: "rpc-" + string(m.ID), Kind: agent.RequestPermission, At: time.Now()}
	pd := pending{id: m.ID, method: m.Method}
	switch m.Method {
	case "item/commandExecution/requestApproval":
		if ownReport(p.Command) {
			c.reply(m.ID, map[string]any{"decision": "accept"}, nil)
			return
		}
		req.Tool, req.Summary = "shell", clip(firstOf(p.Command, p.Reason), maxSummary)
	case "item/fileChange/requestApproval":
		req.Tool, req.Summary = "apply_patch", clip(firstOf(p.Reason, p.GrantRoot), maxSummary)
	case "item/permissions/requestApproval":
		req.Tool, req.Summary, pd.input = "permissions", clip(firstOf(p.Reason, string(p.Permissions)), maxSummary), p.Permissions
	case "item/tool/requestUserInput":
		req.Kind, pd.qids = agent.RequestQuestion, map[string]string{}
		for _, q := range p.Questions {
			aq := agent.Question{Question: q.Question, Header: q.Header}
			for _, o := range q.Options {
				aq.Options = append(aq.Options, o.Label)
			}
			req.Questions = append(req.Questions, aq)
			pd.qids[q.Question] = q.ID
		}
	default:
		c.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "tend does not handle " + m.Method}})
		return
	}
	pd.tool = req.Tool
	c.s.request(req, pd)
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func (c *codexProto) notification(m rpcMessage, at logPos) {
	var p struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		Item struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ClientID string `json:"clientId"`
		} `json:"item"`
		RequestID  json.RawMessage `json:"requestId"`
		TokenUsage struct {
			Total struct {
				Input  int64 `json:"inputTokens"`
				Cached int64 `json:"cachedInputTokens"`
				Output int64 `json:"outputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool `json:"willRetry"`
	}
	json.Unmarshal(m.Params, &p)
	s := c.s
	switch m.Method {
	case "turn/started":
		c.mu.Lock()
		c.turn = p.Turn.ID
		c.mu.Unlock()
		s.mu.Lock()
		s.out.turnDone = time.Time{}
		s.mu.Unlock()
	case "turn/completed":
		c.mu.Lock()
		c.turn = ""
		c.mu.Unlock()
		s.mu.Lock()
		if p.Turn.Status == "failed" && p.Turn.Error != nil {
			s.out.err = clip(p.Turn.Error.Message, maxFinal)
		}
		s.spent(agent.Usage{Turns: 1})
		s.out.turnDone = time.Now()
		s.mu.Unlock()
	case "item/started":
		if p.Item.Type == "userMessage" && p.Item.ClientID != "" {
			s.tookIn(p.Item.ClientID, at)
		}
	case "serverRequest/resolved": // answered, or gone by itself
		if len(p.RequestID) > 0 {
			s.resolved("rpc-"+string(p.RequestID), at)
		}
	case "item/completed":
		if p.Item.Type == "agentMessage" {
			s.mu.Lock()
			s.out.final = clip(p.Item.Text, maxFinal)
			s.said(p.Item.Text)
			s.mu.Unlock()
		}
	case "thread/tokenUsage/updated": // the thread's totals so far
		t := p.TokenUsage.Total
		s.mu.Lock()
		u := agent.Usage{Input: t.Input - t.Cached, CacheRead: t.Cached, Output: t.Output}
		if s.out.usage != nil {
			u.Turns = s.out.usage.Turns
		}
		s.out.usage, s.out.dirty = &u, true
		s.mu.Unlock()
	case "error":
		if !p.WillRetry && p.Error.Message != "" {
			s.mu.Lock()
			s.out.err = clip(p.Error.Message, maxFinal)
			s.mu.Unlock()
		}
	}
}

func (c *codexProto) answer(p pending, a agent.Answer, done func(error)) error {
	decision := "decline"
	if a.Allow {
		decision = "accept"
	}
	switch p.method {
	case "item/permissions/requestApproval":
		if a.Allow {
			return c.reply(p.id, map[string]any{"permissions": p.input, "scope": "turn"}, done)
		}
		return c.reply(p.id, map[string]any{"permissions": map[string]any{}}, done)
	case "item/tool/requestUserInput":
		answers := map[string]any{}
		if a.Allow {
			for q, text := range a.Answers {
				if id := p.qids[q]; id != "" {
					answers[id] = map[string]any{"answers": []string{text}}
				}
			}
		}
		return c.reply(p.id, map[string]any{"answers": answers}, done)
	}
	return c.reply(p.id, map[string]any{"decision": decision}, done)
}

func (c *codexProto) message(id, text string, done func(error)) error {
	c.mu.Lock()
	thread, turn := c.thread, c.turn
	c.mu.Unlock()
	switch {
	case thread == "":
		return errNotReady
	case turn != "":
		steer := turnInput(thread, text, id)
		steer["expectedTurnId"] = turn
		return c.callThen("turn/steer", steer, codexCall{text: text, input: id}, done)
	}
	return c.callThen("turn/start", turnInput(thread, text, id), codexCall{}, done)
}

func (c *codexProto) interrupt(string) {
	c.mu.Lock()
	thread, turn := c.thread, c.turn
	c.mu.Unlock()
	if turn != "" {
		c.call("turn/interrupt", map[string]any{"threadId": thread, "turnId": turn})
	}
}

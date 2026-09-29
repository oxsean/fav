package node

import (
	"cmp"
	"encoding/json"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/tend"
)

func newProto(s *sup) proto {
	if s.spec.Agent == tend.ProviderCodex {
		return &codexProto{s: s}
	}
	return claudeProto{s}
}

// initRequest is the id of the initialize request; its answer carries the signed-in account (initAnswer).
const initRequest = "init"

// claudeProto is claude's stream-json: user messages in, control requests (can_use_tool) out and answered with
// control responses.
type claudeProto struct{ s *sup }

func (c claudeProto) begin(brief string) {
	c.s.in.send(controlRequest(initRequest, map[string]any{"subtype": "initialize", "hooks": nil}))
	c.s.in.send(userMessage(brief))
}

func (c claudeProto) line(text []byte) bool {
	var m struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if json.Unmarshal(text, &m) != nil || m.Type != "control_request" {
		return false
	}
	var r struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		Input       json.RawMessage `json:"input"`
		Description string          `json:"description"`
	}
	if json.Unmarshal(m.Request, &r) != nil || r.Subtype != "can_use_tool" {
		if m.RequestID != "" {
			c.s.in.send(controlError(m.RequestID, "tend does not handle "+r.Subtype))
		}
		return true
	}
	if r.ToolName == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(r.Input, &in) == nil && ownReport(in.Command) {
			c.answer(pending{tool: r.ToolName, input: r.Input}, agent.Answer{Request: m.RequestID, Allow: true})
			return true
		}
	}
	req := agent.Request{ID: m.RequestID, Kind: agent.RequestPermission, Tool: r.ToolName, At: time.Now()}
	if r.ToolName == "AskUserQuestion" {
		req.Kind, req.Questions = agent.RequestQuestion, questionsOf(r.Input)
	} else {
		req.Summary = summaryOf(r.Input, r.Description)
	}
	c.s.request(req, pending{tool: r.ToolName, input: r.Input})
	return true
}

func (c claudeProto) answer(p pending, a agent.Answer) error {
	var resp any
	if a.Allow {
		upd := map[string]any{}
		json.Unmarshal(p.input, &upd)
		if len(a.Answers) > 0 {
			upd["answers"] = a.Answers
		}
		resp = map[string]any{"behavior": "allow", "updatedInput": upd}
	} else {
		resp = map[string]any{"behavior": "deny", "message": cmp.Or(a.Message, "The user denied this.")}
	}
	return c.s.in.send(controlResponse(a.Request, resp))
}

func (c claudeProto) message(text string) error { return c.s.in.send(userMessage(text)) }

func (c claudeProto) interrupt() {
	c.s.in.send(controlRequest("stop", map[string]any{"subtype": "interrupt"}))
}

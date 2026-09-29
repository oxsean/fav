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

// initRequest is the id of the initialize request; its answer carries the signed-in account (scrub).
const initRequest = "init"

// claudeProto is claude's stream-json: user messages in, control requests (can_use_tool) out and answered with
// control responses.
type claudeProto struct{ s *sup }

func (c claudeProto) begin(brief string) {
	c.s.in.send(controlRequest(initRequest, map[string]any{"subtype": "initialize", "hooks": nil}))
	c.s.in.send(userMessage(brief, UUIDFor(c.s.spec.Run, briefInput)))
}

func (c claudeProto) line(text []byte, at logPos) bool {
	var m struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
		UUID      string          `json:"uuid"`
		IsReplay  bool            `json:"isReplay"`
	}
	if json.Unmarshal(text, &m) != nil {
		return false
	}
	if m.Type == "user" && m.IsReplay { // a message it took in, given back; the common reading still sees the turn go on
		if id := c.s.sendOfUUID(m.UUID); id != "" {
			c.s.tookIn(id, at)
		}
		return false
	}
	if m.Type != "control_request" {
		return false
	}
	var r struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		Input       json.RawMessage `json:"input"`
		Description string          `json:"description"`
		Suggestions []struct {
			Type     string       `json:"type"`
			Behavior string       `json:"behavior"`
			Rules    []claudeRule `json:"rules"`
		} `json:"permission_suggestions"`
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
			c.s.in.send(controlResponse(m.RequestID, claudeAnswer(pending{tool: r.ToolName, input: r.Input}, agent.Answer{Allow: true})))
			return true
		}
	}
	req := agent.Request{ID: m.RequestID, Kind: agent.RequestPermission, Tool: r.ToolName, At: time.Now()}
	p := pending{tool: r.ToolName, input: r.Input}
	if r.ToolName == "AskUserQuestion" {
		req.Kind, req.Questions = agent.RequestQuestion, questionsOf(r.Input)
	} else {
		req.Summary = summaryOf(r.Input, r.Description)
		for _, sg := range r.Suggestions {
			if sg.Type == "addRules" && sg.Behavior == "allow" {
				p.rules = append(p.rules, sg.Rules...)
			}
		}
		req.AllowRun = len(p.rules) > 0
	}
	c.s.request(req, p)
	return true
}

func (c claudeProto) answer(p pending, a agent.Answer, done func(error)) error {
	return c.s.in.sendThen(controlResponse(a.Request, claudeAnswer(p, a)), func(err error) {
		if err == nil {
			c.s.resolved(a.Request, logPos{})
		}
		if done != nil {
			done(err)
		}
	})
}

// claudeRule is a permission rule claude suggests: a tool, and for Bash the one command.
type claudeRule struct {
	ToolName    string `json:"toolName"`
	RuleContent string `json:"ruleContent,omitempty"`
}

// claudeAnswer is the control response that answers p with a. Allowed for the run, it adds the rules claude
// suggested to its session alone: ⚠️ another destination writes them into the settings of the repository or the
// user, and the other suggestions (setMode, addDirectories) would allow far more than this command.
func claudeAnswer(p pending, a agent.Answer) any {
	if !a.Allow {
		return map[string]any{"behavior": "deny", "message": cmp.Or(a.Message, "The user denied this.")}
	}
	upd := map[string]any{}
	json.Unmarshal(p.input, &upd)
	if len(a.Answers) > 0 {
		upd["answers"] = a.Answers
	}
	resp := map[string]any{"behavior": "allow", "updatedInput": upd}
	if a.ForRun() && len(p.rules) > 0 {
		resp["updatedPermissions"] = []any{map[string]any{"type": "addRules", "rules": p.rules, "behavior": "allow", "destination": "session"}}
	}
	return resp
}

func (c claudeProto) message(id, text string, done func(error)) error {
	return c.s.in.sendThen(userMessage(text, UUIDFor(c.s.spec.Run, id)), done)
}

func (c claudeProto) interrupt(id string) {
	c.s.in.send(controlRequest(id, map[string]any{"subtype": "interrupt"}))
}

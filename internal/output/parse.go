package output

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Parse reads text, which starts at off in the log file (a line's start) and at st in its turns. It answers the
// events, the state after the last whole line and where that line ends; a last line without its newline is not
// consumed: plain text becomes a temp event, a JSON line waits for the rest.
func Parse(file string, off int64, text string, st State) ([]Event, State, int64) {
	return parse(file, off, text, st, false)
}

// Whole reads one whole line, which starts at off in the log file, as Parse does but with nothing left out of it: a
// tool's output is all there.
func Whole(file string, off int64, line string) []Event {
	evs, _, _ := parse(file, off, strings.TrimSuffix(line, "\n")+"\n", State{}, true)
	return evs
}

func parse(file string, off int64, text string, st State, whole bool) ([]Event, State, int64) {
	if st.Turn < 1 {
		st.Turn = 1
	}
	var out []Event
	for len(text) > 0 {
		line, rest, ended := strings.Cut(text, "\n")
		if !ended {
			if l := strings.TrimRight(line, "\r"); strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "{") {
				out = append(out, Event{Off: off, Kind: KindSay, Temp: true, Key: file + ":" + strconv.FormatInt(off, 10), Text: l, Turn: st.Turn})
			}
			break
		}
		if l := strings.TrimRight(line, "\r"); strings.TrimSpace(l) != "" {
			at := emittedAt(l)
			for n, e := range lineEvents(l, whole) {
				e.At = at
				e.ID, e.Off = file+":"+strconv.FormatInt(off, 10)+":"+strconv.Itoa(n), off
				if opens(e) && st.Closed {
					st.Turn, st.Closed = st.Turn+1, false
				}
				e.Turn = st.Turn
				if e.Kind == KindResult {
					st.Closed = true
				}
				out = append(out, e)
			}
		}
		off += int64(len(line)) + 1
		text = rest
	}
	return out, st, off
}

// emittedAt is when codex says it wrote the line (emittedAtMs), RFC 3339.
func emittedAt(l string) string {
	i := strings.LastIndex(l, `"emittedAtMs":`)
	if i < 0 {
		return ""
	}
	var ms struct {
		At int64 `json:"emittedAtMs"`
	}
	if json.Unmarshal([]byte(l), &ms) != nil || ms.At <= 0 {
		return ""
	}
	return time.UnixMilli(ms.At).Format(time.RFC3339)
}

// opens tells an event that starts a turn once the last one has its result: a turn's start, or what the agent does
// or is told; a tool's result never does.
func opens(e Event) bool {
	switch e.Kind {
	case KindUser, KindSay, KindThink, KindTool, KindCmd, KindEdit, KindMCP:
		return true
	case KindSys:
		return e.Name == "init" || e.Name == "turn/started" || e.Name == "turn.started"
	}
	return false
}

// lineEvents reads one whole line by its shape, whichever provider wrote it.
func lineEvents(l string, whole bool) []Event {
	if !strings.HasPrefix(l, "{") {
		return []Event{{Kind: KindSay, Text: l}}
	}
	var head struct {
		Type   string          `json:"type"`
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(l), &head) != nil {
		return []Event{{Kind: KindSay, Text: l}}
	}
	var evs []Event
	known := true
	switch {
	case claudeTypes[head.Type] || strings.HasPrefix(head.Type, "control_"):
		evs, known = claudeLine(l, whole)
	case head.Type == "" && head.Method != "":
		evs, known = codexNotice(l, head.Method, whole)
	case head.Type == "" && head.ID != nil && (head.Result != nil || head.Error != nil):
		evs = codexAnswer(head.Error)
	case strings.Contains(head.Type, ".") || head.Type == "error":
		evs, known = codexExec(l, head.Type, whole)
	default:
		known = false
	}
	if !known {
		return []Event{{Kind: KindRaw, Text: l}}
	}
	return evs
}

// claudeTypes are the line types of claude's stream-json; those claudeLine does not read are bookkeeping.
var claudeTypes = map[string]bool{"assistant": true, "user": true, "system": true, "result": true, "stream_event": true,
	"command_lifecycle": true, "rate_limit_event": true}

type claudeUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func claudeLine(l string, whole bool) ([]Event, bool) {
	var m struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
		Parent  string `json:"parent_tool_use_id"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Result     string       `json:"result"`
		IsError    bool         `json:"is_error"`
		Cost       float64      `json:"total_cost_usd"`
		DurationMS int64        `json:"duration_ms"`
		Usage      *claudeUsage `json:"usage"`
		RequestID  string       `json:"request_id"`
		UUID       string       `json:"uuid"`
		IsReplay   bool         `json:"isReplay"`
		Request    struct {
			Subtype     string          `json:"subtype"`
			ToolName    string          `json:"tool_name"`
			Input       json.RawMessage `json:"input"`
			Description string          `json:"description"`
		} `json:"request"`
	}
	if json.Unmarshal([]byte(l), &m) != nil {
		return nil, false
	}
	switch m.Type {
	case "assistant", "user":
		evs := claudeContent(m.Type, m.Message.Content, whole)
		for i := range evs {
			evs[i].Parent = m.Parent
			if m.IsReplay && evs[i].Kind == KindUser {
				evs[i].Echo = m.UUID
			}
		}
		return evs, true
	case "system":
		return []Event{{Kind: KindSys, Name: m.Subtype, Model: m.Model}}, true
	case "result":
		e := Event{Kind: KindResult, Text: m.Result, Error: m.IsError, Cost: m.Cost, DurMS: m.DurationMS}
		if e.Text == "" && m.IsError {
			e.Text = m.Subtype
		}
		if u := m.Usage; u != nil {
			e.Usage = &Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
		}
		return []Event{e}, true
	case "control_request":
		if m.Request.Subtype != "can_use_tool" || ownReport(m.Request.ToolName, m.Request.Input) {
			return nil, true
		}
		e := toolEvent(m.Request.ToolName, m.Request.Input)
		e.Family, e.Request = FamilyAsk, m.RequestID
		if e.Title == "" {
			e.Title = oneLine(m.Request.Description)
		}
		return []Event{e}, true
	}
	return nil, true // stream_event and the rest of the control traffic
}

func claudeContent(role string, raw json.RawMessage, whole bool) []Event {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []Event{{Kind: kindOfText(role), Text: text}}
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var evs []Event
	for _, one := range blocks {
		var b claudeBlock
		json.Unmarshal(one, &b)
		switch b.Type {
		case "text":
			evs = append(evs, Event{Kind: kindOfText(role), Text: b.Text})
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				evs = append(evs, Event{Kind: KindThink, Text: b.Thinking})
			}
		case "redacted_thinking":
		case "tool_use":
			e := toolEvent(b.Name, b.Input)
			e.Call = b.ID
			evs = append(evs, e)
		case "tool_result":
			e := resultEvent(claudeResultText(b.Content), whole)
			e.Ref, e.Error = b.ToolUseID, b.IsError
			evs = append(evs, e)
		default:
			evs = append(evs, Event{Kind: KindRaw, Text: string(one)})
		}
	}
	return evs
}

func kindOfText(role string) string {
	if role == "user" {
		return KindUser
	}
	return KindSay
}

func claudeResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// toolEvent is a call of the tool name with input, its family and title from the one table.
func toolEvent(name string, input json.RawMessage) Event {
	e := Event{Kind: KindTool, Tool: name, Input: input, Family: FamilyOf(name)}
	e.Title, e.More = titleOf(e.Family, name, input)
	return e
}

// resultEvent is a tool's result: the output's first and last lines, how long it was, what was left out.
func resultEvent(output string, whole bool) Event {
	e := Event{Kind: KindToolResult}
	e.Output, e.Lines, e.Bytes, e.Truncated = shown(output, whole)
	return e
}

type codexItem struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
	Text     string `json:"text"`
	Content  []struct {
		Text string `json:"text"`
	} `json:"content"`
	Summary          []json.RawMessage `json:"summary"`
	Command          string            `json:"command"`
	AggregatedOutput *string           `json:"aggregatedOutput"`
	AggregatedOut2   *string           `json:"aggregated_output"`
	ExitCode         *int              `json:"exitCode"`
	ExitCode2        *int              `json:"exit_code"`
	DurationMS       int64             `json:"durationMs"`
	Status           string            `json:"status"`
	Changes          []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	} `json:"changes"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content []struct {
			Text *string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Query   string          `json:"query"`
	Items   json.RawMessage `json:"items"`
	Message string          `json:"message"`
}

func codexNotice(l, method string, whole bool) ([]Event, bool) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	json.Unmarshal([]byte(l), &m)
	var p struct {
		Item    *codexItem `json:"item"`
		Message string     `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		Command   string `json:"command"`
		Reason    string `json:"reason"`
		GrantRoot string `json:"grantRoot"`
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
		TokenUsage struct {
			Total struct {
				Input  int64 `json:"inputTokens"`
				Cached int64 `json:"cachedInputTokens"`
				Output int64 `json:"outputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
		Plan json.RawMessage `json:"plan"`
	}
	json.Unmarshal(m.Params, &p)
	switch {
	case method == "item/completed" && p.Item != nil:
		return codexItemEvents(*p.Item, whole)
	case method == "item/completed", method == "item/started", method == "item/updated", strings.HasSuffix(strings.ToLower(method), "delta"):
		return nil, true
	case strings.HasPrefix(method, "item/") && (strings.HasSuffix(method, "/requestApproval") || method == "item/tool/requestUserInput"):
		if method == "item/commandExecution/requestApproval" && ownReport("Bash", commandInput(p.Command)) {
			return nil, true
		}
		e := Event{Kind: KindTool, Family: FamilyAsk, Request: "rpc-" + string(m.ID), Input: m.Params}
		switch method {
		case "item/commandExecution/requestApproval":
			e.Tool = "shell"
			e.Title, e.More = shellTitle(p.Command)
			if e.Title == "" {
				e.Title = oneLine(p.Reason)
			}
		case "item/fileChange/requestApproval":
			e.Tool, e.Title = "apply_patch", oneLine(firstOf(p.Reason, p.GrantRoot))
		case "item/permissions/requestApproval":
			e.Tool, e.Title = "permissions", oneLine(p.Reason)
		case "item/tool/requestUserInput":
			e.Tool = "question"
			if len(p.Questions) > 0 {
				e.Title, e.More = oneLine(p.Questions[0].Question), len(p.Questions)-1
			}
		default:
			e.Tool = strings.TrimPrefix(strings.TrimSuffix(method, "/requestApproval"), "item/")
		}
		return []Event{e}, true
	case method == "turn/plan/updated":
		e := Event{Kind: KindTool, Tool: "update_plan", Family: FamilyOf("update_plan"), Input: m.Params}
		e.Title = planTitle(p.Plan, "step", "status")
		return []Event{e}, true
	case method == "warning":
		return []Event{{Kind: KindSys, Name: method, Level: "warning", Text: firstOf(p.Message, p.Error.Message)}}, true
	case method == "error":
		text := firstOf(p.Error.Message, p.Message)
		if p.WillRetry {
			return []Event{{Kind: KindSys, Name: method, Level: "warning", Text: text}}, true
		}
		return []Event{{Kind: KindError, Text: text}}, true
	case method == "deprecationNotice":
		var d struct {
			Summary string `json:"summary"`
		}
		json.Unmarshal(m.Params, &d)
		return []Event{{Kind: KindSys, Name: method, Text: d.Summary}}, true
	case method == "thread/started", method == "turn/started":
		return []Event{{Kind: KindSys, Name: method}}, true
	case method == "thread/tokenUsage/updated":
		t := p.TokenUsage.Total
		return []Event{{Kind: KindSys, Name: "usage", Usage: &Usage{Input: t.Input - t.Cached, CacheRead: t.Cached, Output: t.Output}}}, true
	case method == "turn/completed":
		e := Event{Kind: KindResult, Error: p.Turn.Status == "failed" || p.Turn.Error != nil}
		if p.Turn.Error != nil {
			e.Text = p.Turn.Error.Message
		}
		return []Event{e}, true
	}
	for _, noise := range codexNoise {
		if strings.HasPrefix(method, noise) {
			return nil, true
		}
	}
	return nil, false
}

// codexNoise is the app-server's bookkeeping, which says nothing a reader follows.
var codexNoise = []string{"hook/", "mcpServer/", "account/", "remoteControl/", "serverRequest/", "thread/status/", "thread/goal/", "turn/diff/"}

func codexAnswer(errRaw json.RawMessage) []Event {
	if errRaw == nil || string(errRaw) == "null" {
		return nil
	}
	var e struct {
		Message string `json:"message"`
	}
	json.Unmarshal(errRaw, &e)
	return []Event{{Kind: KindError, Text: firstOf(e.Message, string(errRaw))}}
}

func codexExec(l, typ string, whole bool) ([]Event, bool) {
	var m struct {
		Item  *codexItem `json:"item"`
		Usage *struct {
			Input  int64 `json:"input_tokens"`
			Cached int64 `json:"cached_input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	json.Unmarshal([]byte(l), &m)
	switch typ {
	case "thread.started", "turn.started":
		return []Event{{Kind: KindSys, Name: typ}}, true
	case "turn.completed":
		e := Event{Kind: KindResult}
		if u := m.Usage; u != nil {
			e.Usage = &Usage{Input: u.Input - u.Cached, CacheRead: u.Cached, Output: u.Output}
		}
		return []Event{e}, true
	case "turn.failed":
		e := Event{Kind: KindResult, Error: true}
		if m.Error != nil {
			e.Text = m.Error.Message
		}
		return []Event{e}, true
	case "error":
		return []Event{{Kind: KindError, Text: m.Message}}, true
	case "item.started", "item.updated":
		return nil, true
	case "item.completed":
		if m.Item != nil {
			return codexItemEvents(*m.Item, whole)
		}
		return nil, true
	}
	return nil, false
}

// codexItemEvents reads a finished item, in the app-server's camelCase or exec's snake_case.
func codexItemEvents(it codexItem, whole bool) ([]Event, bool) {
	switch it.Type {
	case "userMessage":
		var texts []string
		for _, c := range it.Content {
			texts = append(texts, c.Text)
		}
		return []Event{{Kind: KindUser, Text: strings.Join(texts, "\n"), Echo: it.ClientID}}, true
	case "agentMessage", "agent_message":
		return []Event{{Kind: KindSay, Text: it.Text}}, true
	case "reasoning":
		text := it.Text
		if len(it.Summary) > 0 {
			var parts []string
			for _, s := range it.Summary {
				var str string
				if json.Unmarshal(s, &str) != nil {
					var o struct {
						Text string `json:"text"`
					}
					json.Unmarshal(s, &o)
					str = o.Text
				}
				parts = append(parts, str)
			}
			text = strings.Join(parts, "\n")
		}
		if strings.TrimSpace(text) == "" {
			return nil, true
		}
		return []Event{{Kind: KindThink, Text: text}}, true
	case "commandExecution", "command_execution":
		e := Event{Kind: KindCmd, Tool: it.Type, Call: it.ID, Family: FamilyOf(it.Type), Input: commandInput(it.Command), DurMS: it.DurationMS}
		e.Title, e.More = shellTitle(it.Command)
		e.Output, e.Lines, e.Bytes, e.Truncated = shown(deref(firstPtr(it.AggregatedOutput, it.AggregatedOut2)), whole)
		e.Exit = firstPtr(it.ExitCode, it.ExitCode2)
		e.Error = e.Exit != nil && *e.Exit != 0 || it.Status == "failed" || it.Status == "declined"
		return []Event{e}, true
	case "fileChange", "file_change":
		e := Event{Kind: KindEdit, Tool: it.Type, Call: it.ID, Family: FamilyOf(it.Type), Error: it.Status == "failed" || it.Status == "declined"}
		var diffs []string
		for _, c := range it.Changes {
			e.Files = append(e.Files, c.Path)
			if c.Diff != "" {
				diffs = append(diffs, c.Diff)
			}
		}
		e.Diff = strings.Join(diffs, "\n")
		if len(e.Files) > 0 {
			e.Title, e.More = e.Files[0], len(e.Files)-1
			if e.Diff != "" {
				e.Title += " " + counts(diffCounts(e.Diff))
			}
		}
		return []Event{e}, true
	case "mcpToolCall", "mcp_tool_call":
		e := Event{Kind: KindMCP, Tool: it.Tool, Server: it.Server, Call: it.ID, Family: FamilyMCP, Input: it.Arguments}
		e.Title = mcpTitle(it.Server, it.Tool, it.Arguments)
		var out []string
		if it.Result != nil {
			for _, c := range it.Result.Content {
				if c.Text != nil {
					out = append(out, *c.Text)
				}
			}
		}
		if it.Error != nil {
			e.Error = true
			out = append(out, it.Error.Message)
		}
		e.Output, e.Lines, e.Bytes, e.Truncated = shown(strings.Join(out, "\n"), whole)
		return []Event{e}, true
	case "webSearch", "web_search":
		b, _ := json.Marshal(map[string]string{"query": it.Query})
		e := Event{Kind: KindTool, Tool: it.Type, Call: it.ID, Family: FamilyOf(it.Type), Input: b, Title: oneLine(it.Query)}
		return []Event{e}, true
	case "todo_list":
		e := Event{Kind: KindTool, Tool: it.Type, Call: it.ID, Family: FamilyOf(it.Type), Input: it.Items}
		e.Title = planTitle(it.Items, "text", "completed")
		return []Event{e}, true
	case "error":
		return []Event{{Kind: KindError, Text: it.Message}}, true
	}
	return nil, false
}

func commandInput(command string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": command})
	return b
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstPtr[T any](ps ...*T) *T {
	for _, p := range ps {
		if p != nil {
			return p
		}
	}
	return nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

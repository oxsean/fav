// Package output reads a run's output.log into events: claude's stream-json, codex's app-server and exec JSON, plain
// text (docs/design/runs/output.md).
package output

import "encoding/json"

// Kinds of Event.
const (
	KindUser       = "user"
	KindSay        = "say"
	KindThink      = "think"
	KindTool       = "tool"
	KindToolResult = "tool_result"
	KindCmd        = "cmd"
	KindEdit       = "edit"
	KindMCP        = "mcp"
	KindSys        = "sys"
	KindResult     = "result"
	KindError      = "error"
	KindRaw        = "raw"
)

// Families of tool events.
const (
	FamilyShell  = "shell"
	FamilyRead   = "read"
	FamilySearch = "search"
	FamilyEdit   = "edit"
	FamilyWeb    = "web"
	FamilyAgent  = "agent"
	FamilyPlan   = "plan"
	FamilyAsk    = "ask"
	FamilyMCP    = "mcp"
	FamilyOther  = "other"
)

// Event is one thing a run's output says. ID is "file:off:n": the log's identity, where the line starts, the event's
// place in the line; a temp event has a Key instead and is replaced by the next one with that key.
type Event struct {
	ID     string `json:"id,omitempty"`
	Off    int64  `json:"off"`
	Kind   string `json:"kind"`
	Temp   bool   `json:"temp,omitempty"`
	Key    string `json:"key,omitempty"`
	Stream string `json:"stream,omitempty"`
	Turn   int    `json:"turn,omitempty"`
	Parent string `json:"parent,omitempty"`
	At     string `json:"at,omitempty"`

	Text  string `json:"text,omitempty"`
	Name  string `json:"name,omitempty"`
	Level string `json:"level,omitempty"`
	Model string `json:"model,omitempty"`

	Tool    string          `json:"tool,omitempty"`
	Server  string          `json:"server,omitempty"`
	Call    string          `json:"call,omitempty"`
	Request string          `json:"request,omitempty"`
	Family  string          `json:"family,omitempty"`
	Title   string          `json:"title,omitempty"`
	More    int             `json:"more,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Files   []string        `json:"files,omitempty"`
	Diff    string          `json:"diff,omitempty"`

	Ref       string         `json:"ref,omitempty"`
	Output    string         `json:"output,omitempty"`
	Exit      *int           `json:"exit,omitempty"`
	DurMS     int64          `json:"dur_ms,omitempty"`
	Lines     int            `json:"lines,omitempty"`
	Bytes     int            `json:"bytes,omitempty"`
	Error     bool           `json:"error,omitempty"`
	Truncated map[string]int `json:"truncated,omitempty"`

	Usage *Usage  `json:"usage,omitempty"`
	Cost  float64 `json:"cost,omitempty"`
}

// Usage is tokens as a line reports them: claude's result for its turn, codex's thread totals so far.
type Usage struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

// State is where a page starts in the run's turns: Turn is the turn in effect, Closed that its result came.
type State struct {
	Turn   int  `json:"turn"`
	Closed bool `json:"closed,omitempty"`
}

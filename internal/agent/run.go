package agent

import "time"

// Usage is what a run's agent spent, as its CLI reported it.
type Usage struct {
	Input      int64   `json:"input,omitempty"` // tokens sent that no cache served
	CacheRead  int64   `json:"cache_read,omitempty"`
	CacheWrite int64   `json:"cache_write,omitempty"`
	Output     int64   `json:"output,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"` // the CLI's own estimate (claude); 0 when it gives none
	Turns      int     `json:"turns,omitempty"`
}

// Add is u with one turn's usage added.
func (u Usage) Add(t Usage) Usage {
	u.Input += t.Input
	u.CacheRead += t.CacheRead
	u.CacheWrite += t.CacheWrite
	u.Output += t.Output
	u.Turns += t.Turns
	if t.CostUSD > 0 {
		u.CostUSD = t.CostUSD // ⚠️ claude reports the whole process's cost so far, not the turn's
	}
	return u
}

// Request kinds.
const (
	RequestPermission = "permission" // a tool it wants to use
	RequestQuestion   = "question"   // questions for the user (claude's AskUserQuestion)
)

// Request is something a running agent waits on.
type Request struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Tool      string     `json:"tool,omitempty"`
	Summary   string     `json:"summary,omitempty"` // what the tool would do: its command, file or description
	Questions []Question `json:"questions,omitempty"`
	At        time.Time  `json:"at"`
}

type Question struct {
	Question string   `json:"question"`
	Header   string   `json:"header,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multi    bool     `json:"multi,omitempty"`
}

// Answer is the user's answer to a Request.
type Answer struct {
	Request string            `json:"request"`
	Allow   bool              `json:"allow"`
	Message string            `json:"message,omitempty"` // why it was denied
	Answers map[string]string `json:"answers,omitempty"` // question → the chosen option labels (", " between) or own words
}

// Send states.
const (
	SendQueued = "queued"
	SendSent   = "sent"   // written to the agent
	SendFailed = "failed" // the agent ended before it went out
)

// Send is a message for a running agent and how far it got.
type Send struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

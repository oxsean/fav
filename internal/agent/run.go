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
	AllowRun  bool       `json:"allow_run,omitempty"` // it can be allowed for the rest of the run (DecisionAllowRun)
	Failed    bool       `json:"failed,omitempty"`    // an answer did not reach the agent: it waits to be answered again
	At        time.Time  `json:"at"`
}

type Question struct {
	Question string   `json:"question"`
	Header   string   `json:"header,omitempty"`
	Options  []string `json:"options,omitempty"`
	Multi    bool     `json:"multi,omitempty"`
}

// Decisions on a permission. Allow says the same for an end that knows no Decision: allow_run is an allow there.
const (
	DecisionAllow    = "allow"     // this once
	DecisionAllowRun = "allow_run" // this command, for the rest of the run: never past its process, never into settings
	DecisionDeny     = "deny"
)

// Answer is the user's answer to a Request.
type Answer struct {
	Request  string            `json:"request"`
	Allow    bool              `json:"allow"`
	Decision string            `json:"decision,omitempty"` // "" is Allow's
	Message  string            `json:"message,omitempty"`  // why it was denied
	Answers  map[string]string `json:"answers,omitempty"`  // question → the chosen option labels (", " between) or own words
	By       string            `json:"by,omitempty"`       // who answered
}

// Settled is a with Allow as its decision says.
func (a Answer) Settled() Answer {
	switch a.Decision {
	case DecisionAllow, DecisionAllowRun:
		a.Allow = true
	case DecisionDeny:
		a.Allow = false
	}
	return a
}

// ForRun: a allows its request for the rest of the run.
func (a Answer) ForRun() bool { return a.Allow && a.Decision == DecisionAllowRun }

// RunCaps is what a started run can do, as its node found it.
type RunCaps struct {
	Steer       bool `json:"steer,omitempty"`        // a message joins the running turn
	After       bool `json:"after,omitempty"`        // a message after the turn continues its session
	Interrupt   bool `json:"interrupt,omitempty"`    // a turn can be interrupted (run.interrupt)
	AnswerScope bool `json:"answer_scope,omitempty"` // a permission can be allowed for the run (DecisionAllowRun)
	Questions   bool `json:"questions,omitempty"`    // its questions are answered while it runs
	Continue    bool `json:"continue,omitempty"`     // once ended, its session goes on in another run
	Takeover    bool `json:"takeover,omitempty"`     // once ended, its session resumes in a terminal
}

// Send states.
const (
	SendQueued = "queued"
	SendSent   = "sent"   // written to the agent
	SendSeen   = "seen"   // the agent took it in: its output gave the message back
	SendFailed = "failed" // the agent ended before it went out
)

// Send modes: how a message for a running run goes in.
const (
	SendSteer     = "steer"     // into the running turn ("" too)
	SendAfter     = "after"     // once the turn ended and the run exited, a new run continues its session with it
	SendInterrupt = "interrupt" // the turn is interrupted first, then as after
)

// Send is a message for a running agent and how far it got. The coordinator keeps an after or interrupt message
// itself: it never goes to the node.
type Send struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	State string    `json:"state"`
	Mode  string    `json:"mode,omitempty"`
	By    string    `json:"by,omitempty"` // who sent it
	At    time.Time `json:"at"`
}

// Carried: the coordinator keeps s for the run that continues the one it was sent to.
func (s Send) Carried() bool { return s.Mode == SendAfter || s.Mode == SendInterrupt }

// Verdicts a run reports on the work it was given to judge (tend run verdict).
const (
	VerdictPass    = "pass"
	VerdictRework  = "rework"
	VerdictBlocked = "blocked"
)

// Verdict is what a review or test run concluded.
type Verdict struct {
	Verdict string    `json:"verdict"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at"`
}

// CheckResult is how a stage's check hook went: it runs where the agent worked, after the agent exits well.
type CheckResult struct {
	Argv []string `json:"argv"`
	Exit int      `json:"exit"`
	Tail string   `json:"tail,omitempty"` // the end of what it printed
}

// Work is what a run did to its task's branch, as the node recorded it: where it worked, the branch's head after it (a
// read-only run: the commit it looked at), what the branch holds since it forked, and what went wrong on the way. A
// merge run says whether it merged or which files were in conflict.
type Work struct {
	Dir       string   `json:"dir,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Head      string   `json:"head,omitempty"`
	Commits   int      `json:"commits,omitempty"`
	Diffstat  string   `json:"diffstat,omitempty"`
	Discarded int      `json:"discarded,omitempty"` // files a read-only run changed, thrown away
	Merged    bool     `json:"merged,omitempty"`
	Conflict  []string `json:"conflict,omitempty"`
	PR        string   `json:"pr,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Workspace is where a run works in git (a node's feature worktree): task branch Branch in a worktree beside the project's checkout, the branch
// made from the last of Chain (its ancestors' integration branches, outermost first, the first made from Base). With
// Remote the branches are fetched before and pushed after, so another machine can go on with them.
type Workspace struct {
	Checkout string   `json:"checkout"`
	Branch   string   `json:"branch"`
	Chain    []string `json:"chain,omitempty"`
	Base     string   `json:"base,omitempty"` // default: the checkout's HEAD
	Remote   string   `json:"remote,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"` // a detached copy of the branch's head, thrown away after
	Merge    string   `json:"merge,omitempty"`     // a merge run: this branch goes into Branch; no agent runs
	Setup    []string `json:"setup,omitempty"`     // run in a worktree once it is made
	// BeforeRun runs in the task's worktree before each run's agent starts.
	BeforeRun []string `json:"before_run,omitempty"`
	Cleanup   []string `json:"cleanup,omitempty"` // run in a merged branch's worktree before it is removed
}

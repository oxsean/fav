package coord

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/task"
)

// Notify events: a run wants someone.
const (
	NotifyWaiting = "run.waiting" // it ended on a question or a denied permission: a reply continues it
	NotifyAsked   = "run.asked"   // it asked a question and runs on
	NotifyFailed  = "run.failed"  // it failed, or exited with a code other than 0
	NotifyStalled = "run.stalled" // it has said nothing for a long time
)

// NotifyEvent is what config's notify_command reads on stdin, one JSON object.
type NotifyEvent struct {
	Event    string    `json:"event"`
	Run      string    `json:"run"`
	Task     string    `json:"task"`
	Title    string    `json:"title,omitempty"`
	Machine  string    `json:"machine"`
	Agent    string    `json:"agent"`
	State    string    `json:"state"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Ask      string    `json:"ask,omitempty"`
	Session  string    `json:"session,omitempty"`
	Dir      string    `json:"dir,omitempty"`
	At       time.Time `json:"at"`
}

// maxNotify keeps an event within one pipe buffer (4 KiB on Windows): it is written before the command starts.
const maxNotify = 3500

// observed are copies of the runs events observe, as they stand before the events apply; the caller holds mu.
func (c *Coord) observed(events []journal.Event) map[string]task.Run {
	if len(c.opt.Config.NotifyCommand) == 0 {
		return nil
	}
	out := map[string]task.Run{}
	for _, e := range events {
		if e.Type != task.ERunObserved {
			continue
		}
		var o task.Observation
		if json.Unmarshal(e.Data, &o) == nil {
			if r := c.st.Runs[o.ID]; r != nil {
				if _, seen := out[o.ID]; !seen {
					out[o.ID] = *r
				}
			}
		}
	}
	return out
}

// notify runs the notify command for each run that came to want someone; the caller holds mu.
func (c *Coord) notify(before map[string]task.Run) {
	for id, was := range before {
		r := c.st.Runs[id]
		if r == nil {
			continue
		}
		if ev := notifyEvent(&was, r); ev != "" && c.notifies(ev) {
			c.runNotify(ev, r)
		}
	}
}

// notifyEvent is what moved from was to now, "" when nothing anyone needs to hear.
func notifyEvent(was, now *task.Run) string {
	failed := func(r *task.Run) bool {
		return r.State == task.Failed || r.State == task.Exited && r.ExitCode != nil && *r.ExitCode != 0 && !r.Waiting()
	}
	switch {
	case failed(now) && !failed(was):
		return NotifyFailed
	case now.Waiting() && !was.Waiting():
		return NotifyWaiting
	case task.Open(now.State) && now.Attention == task.AttentionAsked && was.Attention != task.AttentionAsked:
		return NotifyAsked
	case task.Open(now.State) && now.Attention == task.AttentionStalled && was.Attention != task.AttentionStalled:
		return NotifyStalled
	}
	return ""
}

func (c *Coord) notifies(ev string) bool {
	return len(c.opt.Config.NotifyEvents) == 0 || slices.Contains(c.opt.Config.NotifyEvents, ev)
}

// runNotify starts the notify command with ev on stdin and lets it go: it outlives a one-shot coordinator.
func (c *Coord) runNotify(ev string, r *task.Run) {
	e := NotifyEvent{Event: ev, Run: r.ID, Task: r.Task, Title: clip(r.Title, 300), Machine: r.Machine, Agent: r.Agent, State: r.State,
		ExitCode: r.ExitCode, Reason: r.Reason, Detail: clip(r.Detail, 500), Ask: clip(r.Ask, 1500), Session: r.Session, Dir: r.Dir,
		At: time.Now()}
	b, err := json.Marshal(e)
	if err != nil || len(b) > maxNotify {
		e.Ask, e.Detail, e.Title = clip(e.Ask, 300), clip(e.Detail, 200), clip(e.Title, 100)
		if b, err = json.Marshal(e); err != nil {
			return
		}
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return
	}
	defer pr.Close()
	pw.Write(append(b, '\n'))
	pw.Close()
	argv := c.opt.Config.NotifyCommand
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = pr
	cmd.Env = append(os.Environ(), "TEND_EVENT="+ev, "TEND_RUN="+r.ID, "TEND_TASK="+r.Task)
	if proc.StartDetached(cmd) == nil {
		cmd.Process.Release()
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}

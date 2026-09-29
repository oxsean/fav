package coord

import (
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
)

// Task notify events.
const (
	NotifyTaskWaiting = "task.needs_you" // a task came to wait for someone
	NotifyTaskDone    = "task.done"
	NotifyTaskStage   = "task.stage"  // a task in a workflow moved on to a stage; only the notify command hears it
	NotifyTaskRework  = "task.rework" // a stage sent a task back; only the notify command hears it
)

// Notice is a task that came to need someone, for the people it concerns.
type Notice struct {
	Seq     int64     `json:"seq"`
	Event   string    `json:"event"`
	Task    string    `json:"task"`
	Title   string    `json:"title"`
	Project string    `json:"project,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Run     string    `json:"run,omitempty"`
	Stage   string    `json:"stage,omitempty"`
	To      []string  `json:"to"` // user ids
	At      time.Time `json:"at"`
}

// InboxItem is a task waiting for the caller.
type InboxItem struct {
	Task    string         `json:"task"`
	Title   string         `json:"title"`
	Project string         `json:"project,omitempty"`
	Reason  string         `json:"reason"`
	Run     string         `json:"run,omitempty"`
	Since   time.Time      `json:"since"`
	As      []string       `json:"as,omitempty"`
	Pending []task.Pending `json:"pending,omitempty"` // what exactly waits: the requests the viewer may answer, else the one thing
}

// Roles in InboxItem.As: why an item waits for the viewer.
const (
	AsOwner      = "owner"
	AsApprover   = "approver"
	AsDispatcher = "dispatcher"
	AsAdmin      = "admin" // its run is still open on a retired machine
)

// roles are why task t's situation sit is for user: owner, approver (its Accepter), dispatcher; the owner when nobody else is named.
func roles(st *task.State, t *task.Task, sit task.Situation, user string) []string {
	var out []string
	if t.Owner == user {
		out = append(out, AsOwner)
	}
	if sit.Reason == task.WhyAccept && t.Accepter() == user {
		out = append(out, AsApprover)
	}
	if r := st.Runs[sit.Run]; r != nil && r.Dispatcher == user {
		out = append(out, AsDispatcher)
	}
	if len(out) == 0 {
		out = append(out, AsOwner)
	}
	return out
}

type Inbox struct {
	Items []InboxItem `json:"items"`
}

// concerns are the people task t's situation sit is for: its owner, its approver when it is to be accepted, and
// whoever dispatched the run it is about.
func concerns(st *task.State, t *task.Task, sit task.Situation) []string {
	var out []string
	add := func(u string) {
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	add(t.Owner)
	if sit.Reason == task.WhyAccept {
		add(t.Accepter())
	}
	if r := st.Runs[sit.Run]; r != nil {
		add(r.Dispatcher)
	}
	if len(out) == 0 {
		add(Owner.User)
	}
	return out
}

// touched are the tasks events are about; the caller holds mu.
func (c *Coord) touched(events []journal.Event) []string {
	var ids []string
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, e := range events {
		var s subject
		json.Unmarshal(e.Data, &s)
		switch {
		case e.Type == task.ETaskStarted:
			var d task.TaskStart
			json.Unmarshal(e.Data, &d)
			for _, id := range d.IDs {
				add(id)
			}
		case s.Task != "":
			add(s.Task)
		case c.st.Runs[s.ID] != nil:
			add(c.st.Runs[s.ID].Task)
		case c.st.Tasks[s.ID] != nil || e.Type == task.ETaskCreated:
			add(s.ID)
		}
		if t := c.st.Tasks[s.ID]; t != nil { // a subtask's end moves its parent
			add(t.Parent)
		}
	}
	return ids
}

// situations are how the tasks ids stand now; the caller holds mu.
func (c *Coord) situations(ids []string) map[string]task.Situation {
	out := map[string]task.Situation{}
	for _, id := range ids {
		if t := c.st.Tasks[id]; t != nil {
			out[id] = c.st.Situation(t)
		}
	}
	return out
}

// notices are the tasks among before's that came to wait for someone, or got done, in env; the caller holds mu.
func (c *Coord) notices(env journal.Envelope, before map[string]task.Situation) []Notice {
	var out []Notice
	ids := make([]string, 0, len(before))
	for id := range before {
		ids = append(ids, id)
	}
	for _, e := range env.Events { // tasks made in env; workflow moves
		switch e.Type {
		case task.ETaskCreated:
			var s subject
			json.Unmarshal(e.Data, &s)
			if _, ok := before[s.ID]; !ok {
				ids = append(ids, s.ID)
			}
		case task.ETaskStaged:
			var d task.TaskStage
			json.Unmarshal(e.Data, &d)
			if t := c.st.Tasks[d.ID]; t != nil {
				ev := NotifyTaskStage
				if d.Back {
					ev = NotifyTaskRework
				}
				out = append(out, Notice{Seq: env.Seq, Event: ev, Task: t.ID, Title: t.Title, Project: t.Project, Stage: d.Stage, At: env.At})
			}
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := c.st.Tasks[id]
		if t == nil {
			continue
		}
		was, now := before[id], c.st.Situation(t)
		ev := ""
		switch {
		case now.Kind == task.SitWaiting && now.Reason != task.WhyDispatch && (was.Kind != now.Kind || was.Reason != now.Reason):
			ev = NotifyTaskWaiting
		case now.Kind == task.SitDone && was.Kind != task.SitDone && was.Kind != "":
			ev = NotifyTaskDone
		}
		if ev != "" {
			out = append(out, Notice{Seq: env.Seq, Event: ev, Task: id, Title: t.Title, Project: t.Project, Reason: now.Reason,
				Run: now.Run, To: concerns(c.st, t, now), At: env.At})
		}
	}
	return out
}

// deliver hands the notices on: to the server's delivery (Options.Notice) and to the notify command.
func (c *Coord) deliver(ns []Notice) {
	for _, n := range ns {
		if c.opt.Notice != nil && len(n.To) > 0 {
			c.opt.Notice(n)
		}
		if len(c.opt.Config.NotifyCommand) > 0 && c.notifies(n.Event) {
			c.runNotifyEvent(NotifyEvent{Event: n.Event, Task: n.Task, Title: clip(n.Title, 300), Reason: n.Reason, Run: n.Run, Stage: n.Stage, At: n.At},
				"TEND_TASK="+n.Task)
		}
	}
}

// inbox is what waits for p: the tasks waiting for someone (not merely for a dispatch nobody asked for) that concern p
// and that p may act on, longest waiting first.
func (c *Coord) inbox(p Principal) Inbox {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Inbox{Items: []InboxItem{}}
	for _, t := range c.st.Tasks {
		sit := c.st.Situation(t)
		if r := c.st.Runs[sit.Run]; p.Admin && r != nil && task.Open(r.State) && c.retired(r.Machine) {
			out.Items = append(out.Items, InboxItem{Task: t.ID, Title: t.Title, Project: t.Project, Reason: sit.Reason, Run: r.ID, Since: r.Since(),
				As: []string{AsAdmin}})
			continue
		}
		if sit.Kind != task.SitWaiting || sit.Reason == task.WhyDispatch || !canWrite(c.st, p, t) || !slices.Contains(concerns(c.st, t, sit), p.User) {
			continue
		}
		since := t.UpdatedAt
		if r := c.st.Runs[sit.Run]; r != nil {
			since = r.Since()
		}
		out.Items = append(out.Items, InboxItem{Task: t.ID, Title: t.Title, Project: t.Project, Reason: sit.Reason, Run: sit.Run, Since: since,
			As: roles(c.st, t, sit, p.User), Pending: c.pendingFor(p, t)})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Since.Before(out.Items[j].Since) })
	return out
}

// pendingFor is what waits on p about t: a permission only for those who may grant it. The caller holds mu.
func (c *Coord) pendingFor(p Principal, t *task.Task) []task.Pending {
	return slices.DeleteFunc(c.st.Pending(t), func(x task.Pending) bool {
		r := c.st.Runs[x.Run]
		return x.Kind == task.PendPermission && x.Request != "" && r != nil && !c.canApprove(p, r)
	})
}

package coord

import (
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// TaskRef names one task.
type TaskRef struct {
	ID string `json:"id"`
}

const (
	maxAfter  = 20
	maxAccept = 50
	maxTags   = 20
	maxLine   = 1000
)

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefs(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

// checkFields checks what a task says about itself besides its place. The caller holds mu.
func (c *Coord) checkFields(who Principal, project, kind, approver string, accept, tags []string) error {
	if kind != "" && kind != "requirement" {
		return bad("kind " + kind)
	}
	if len(accept) > maxAccept || slices.ContainsFunc(accept, func(s string) bool { return len(s) > maxLine }) {
		return bad("acceptance")
	}
	if len(tags) > maxTags || slices.ContainsFunc(tags, func(s string) bool { return s == "" || len(s) > 64 }) {
		return bad("tags")
	}
	if approver != "" && approver != who.User {
		return c.checkActor(project, approver)
	}
	return nil
}

// checkActor: user may own or accept a task of project. The caller holds mu.
func (c *Coord) checkActor(project, user string) error {
	if err := c.checkUser(user); err != nil {
		return err
	}
	u, _ := c.user(user)
	switch {
	case project == "" && !u.Admin:
		return forbidden("a task outside projects stays with its owner")
	case project != "" && roleIn(c.st, Principal{User: u.ID, Admin: u.Admin}, project) != task.RoleParticipant:
		return forbidden("user " + user + " takes no part in project " + project)
	}
	return nil
}

// checkHandOver: who may give t another owner or approver (its owner, its project's owner, an admin), and owner may
// take it. The caller holds mu.
func (c *Coord) checkHandOver(who Principal, t *task.Task, owner *string) error {
	pr := c.st.Projects[t.Project]
	if !who.Admin && t.Owner != who.User && (pr == nil || pr.Owner != who.User) {
		return forbidden("owner " + t.ID)
	}
	if owner != nil && *owner != t.Owner {
		if *owner == "" {
			return bad("owner")
		}
		return c.checkActor(t.Project, *owner)
	}
	return nil
}

// checkPlace: t may sit under parent and after the tasks in after. t need not be in the state yet. The caller holds mu.
func (c *Coord) checkPlace(who Principal, t *task.Task, parent string, after []string) error {
	if parent != "" {
		p, err := c.writableTask(who, parent)
		switch {
		case err != nil:
			return err
		case p.ID == t.ID || c.st.Under(parent, t.ID):
			return bad("parent inside its own subtree")
		case p.Project != t.Project:
			return bad("parent in another project")
		case task.Finished(p.Status):
			return conflict("parent " + p.Status)
		case c.st.OpenRun(p.ID) != nil:
			return conflict("parent has an open run")
		}
		height := 1
		if c.st.Tasks[t.ID] != nil {
			height = c.st.Height(t.ID)
		}
		if c.st.Depth(parent)+height > task.MaxDepth {
			return bad("deeper than 3 levels")
		}
	}
	if len(after) > maxAfter {
		return bad("after")
	}
	for i, a := range after {
		d := c.st.Tasks[a]
		switch {
		case !canRead(c.st, who, d):
			return notFound(a)
		case slices.Contains(after[:i], a):
			return bad("after " + a + " twice")
		case d.Project != t.Project:
			return bad("after a task in another project")
		case a == t.ID || a == parent || parent != "" && c.st.Under(parent, a) || c.st.Under(a, t.ID):
			return bad("after its own ancestor or subtask")
		case c.st.Reaches(a, t.ID):
			return bad("after makes a cycle")
		}
	}
	return nil
}

func (c *Coord) taskStart(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskRef
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	if task.Finished(t.Status) {
		return "", nil, conflict("task " + t.Status)
	}
	var ids []string
	for _, x := range c.st.Subtree(t.ID) {
		if !task.Finished(x.Status) {
			ids = append(ids, x.ID)
		}
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskStarted, task.TaskStart{IDs: ids})}, nil
}

func (c *Coord) taskMove(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.TaskMove
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	parent, after := t.Parent, t.After
	if p.Parent != nil {
		parent = *p.Parent
	}
	if p.After != nil {
		after = *p.After
	}
	if parent == t.Parent && slices.Equal(after, t.After) {
		return t.ID, nil, nil
	}
	if err := c.checkPlace(who, t, parent, after); err != nil {
		return "", nil, err
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskMoved, p)}, nil
}

// cancelTree cancels t and every unfinished task under it, with their queued runs, and asks their running ones to
// stop. The caller holds mu.
func (c *Coord) cancelTree(t *task.Task) []journal.Event {
	var events, runs []journal.Event
	for _, x := range c.st.Subtree(t.ID) {
		if task.Finished(x.Status) {
			continue
		}
		events = append(events, journal.NewEvent(task.ETaskStatus, task.TaskStatus{ID: x.ID, Status: task.StatusCanceled}))
		if r := c.st.OpenRun(x.ID); r != nil {
			switch {
			case r.State == task.Queued:
				runs = append(runs, journal.NewEvent(task.ERunCanceled, task.RunRef{ID: r.ID}))
			case r.Want != "stop":
				runs = append(runs, journal.NewEvent(task.ERunStopAsked, task.RunRef{ID: r.ID}))
			}
		}
	}
	return append(events, runs...)
}

// principal is user as the coordinator acts for them; false when they are gone or disabled. The caller holds mu.
func (c *Coord) principal(user string) (Principal, bool) {
	if !c.team() {
		return Owner, true
	}
	u, ok := c.user(user)
	if !ok || u.Disabled {
		return Principal{}, false
	}
	return Principal{User: u.ID, Admin: u.Admin}, true
}

// flow does what started tasks wait for: marks done the ones whose run succeeded, and dispatches the ready ones as
// their owner would; one that cannot go is held with the reason. The caller holds mu.
func (c *Coord) flow() {
	for range 10 {
		if carried := c.carryOn(); len(carried) > 0 { // before its task counts as ended
			if c.commit(journal.System, nil, carried...) != nil {
				return
			}
			continue
		}
		var events []journal.Event
		for _, t := range c.st.Completing() {
			events = append(events, c.finish(t)...)
		}
		events = append(events, c.stageMoves()...)
		for _, t := range c.st.Ready() {
			who, ok := c.principal(t.Owner)
			run, err := task.Run{}, error(forbidden("owner "+t.Owner))
			switch {
			case ok && t.Flow != nil:
				run, err = c.stagePlan(who, t)
			case ok:
				run, err = c.plan(who, Dispatch{Task: t.ID})
			}
			if err == nil && !c.canUse(who, run.Machine, run.Project) {
				err = forbidden("machine " + run.Machine)
			}
			if err != nil {
				events = append(events, journal.NewEvent(task.ETaskHeld, holdOf(t.ID, err)))
				continue
			}
			events = append(events, journal.NewEvent(task.ERunQueued, run))
		}
		if len(events) == 0 || c.commit(journal.System, nil, events...) != nil {
			return
		}
	}
}

// carryOn continues each ended run's session with the messages sent to go after its turn, as their first sender; the
// messages fail when nothing may go on from it: it was stopped, its task is finished, a later run of the task started,
// or the continuation cannot start. The caller holds mu.
func (c *Coord) carryOn() []journal.Event {
	var ended []*task.Run
	for _, r := range c.st.Runs {
		if !task.Open(r.State) && slices.ContainsFunc(r.Sends, carried) {
			ended = append(ended, r)
		}
	}
	sort.Slice(ended, func(i, j int) bool {
		return ended[i].Seq < ended[j].Seq || ended[i].Seq == ended[j].Seq && ended[i].ID < ended[j].ID
	})
	var events []journal.Event
	for _, r := range ended {
		var sends []agent.Send
		for _, m := range r.Sends {
			if carried(m) {
				sends = append(sends, m)
			}
		}
		t := c.st.Tasks[r.Task]
		run, err := task.Run{}, error(conflict("stopped"))
		if who, ok := c.principal(sends[0].By); ok && r.Want != "stop" && t != nil && !task.Finished(t.Status) && !c.later(r) {
			texts := make([]string, len(sends))
			for i, m := range sends {
				texts[i] = m.Text
			}
			run, err = c.continuation(who, r, strings.Join(texts, "\n\n"), "")
		}
		if err != nil {
			for _, m := range sends {
				m.State = agent.SendFailed
				events = append(events, journal.NewEvent(task.ERunSent, task.RunSend{ID: r.ID, Send: m}))
			}
			continue
		}
		for _, m := range sends {
			run.Takes = append(run.Takes, m.ID)
		}
		events = append(events, journal.NewEvent(task.ERunQueued, run))
	}
	return events
}

func carried(m agent.Send) bool { return m.State == agent.SendQueued && m.Carried() }

// later: another run of r's task was queued after it.
func (c *Coord) later(r *task.Run) bool {
	for _, x := range c.st.Runs {
		if x.Task == r.Task && x.Seq > r.Seq {
			return true
		}
	}
	return false
}

func holdOf(id string, err error) task.TaskHold {
	var we *wire.Error
	if errors.As(err, &we) {
		return task.TaskHold{ID: id, Reason: we.Code, Detail: we.Detail}
	}
	return task.TaskHold{ID: id, Reason: wire.CodeInternal, Detail: err.Error()}
}

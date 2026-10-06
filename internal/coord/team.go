package coord

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

type ProjectCreate struct {
	ID    string `json:"id,omitempty"` // default: a new one
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"` // default: the caller
}

// RunMessages is run.messages: a page of a run's conversation, as remote.MessagesParams pages a session's.
type RunMessages struct {
	Run    string `json:"run"`
	Before int64  `json:"before"`
	N      int    `json:"n"`
	File   string `json:"file,omitempty"`
}

var projectID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// NoProject is never a project's id: clients name the sessions in no project by it.
const NoProject = "none"

const maxName = 128

func projectView(st *task.State, id string) any {
	if p := st.Projects[id]; p != nil {
		cp := *p
		cp.Members = maps.Clone(p.Members)
		return &cp
	}
	return nil
}

// shareView answers machine.share: who else may dispatch there, without its session scope.
func shareView(st *task.State, machine string) any {
	if s := st.Shares[machine]; s != nil {
		cp := *s
		cp.Sessions = nil
		return &cp
	}
	return &task.Share{Machine: machine}
}

// sessionView answers machine.sessions: who else reads the machine's sessions; none is private.
func sessionView(st *task.State, machine string) any {
	out := &task.SessionsSet{Machine: machine}
	if s := st.Shares[machine]; s != nil && s.Sessions != nil {
		out.Users, out.Projects, out.Team = s.Sessions.Users, s.Sessions.Projects, s.Sessions.Team
	}
	return out
}

func (c *Coord) checkUser(id string) error {
	if u, ok := c.user(id); !ok || u.Disabled {
		return notFound("user " + id)
	}
	return nil
}

func (c *Coord) projectCreate(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p ProjectCreate
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > maxName {
		return "", nil, bad("name")
	}
	if p.ID == "" {
		p.ID = newID("p_")
	}
	if !projectID.MatchString(p.ID) || p.ID == NoProject {
		return "", nil, bad("id")
	}
	if c.st.Projects[p.ID] != nil {
		return "", nil, conflict("project " + p.ID)
	}
	p.Owner = firstOf(p.Owner, who.User)
	if !who.Admin && p.Owner != who.User {
		return "", nil, forbidden("owner " + p.Owner)
	}
	if err := c.checkUser(p.Owner); err != nil {
		return "", nil, err
	}
	pr := task.Project{ID: p.ID, Name: p.Name, Owner: p.Owner}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectCreated, pr)}, nil
}

// managedProject is project id when who manages it (its owner, or an admin): not found when they cannot see it.
func (c *Coord) managedProject(who Principal, id string) (*task.Project, error) {
	pr := c.st.Projects[id]
	switch {
	case pr == nil || !c.seesProject(who, pr):
		return nil, notFound("project " + id)
	case !who.Admin && pr.Owner != who.User:
		return nil, forbidden("project " + id)
	}
	return pr, nil
}

func (c *Coord) projectEdit(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.ProjectEdit
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	pr, err := c.managedProject(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	changed := false
	if p.Name != nil {
		if *p.Name = strings.TrimSpace(*p.Name); *p.Name == "" || len(*p.Name) > maxName {
			return "", nil, bad("name")
		}
		changed = changed || *p.Name != pr.Name
	}
	if p.Owner != nil {
		if err := c.checkUser(*p.Owner); err != nil {
			return "", nil, err
		}
		changed = changed || *p.Owner != pr.Owner
	}
	if err := c.checkSettings(p); err != nil {
		return "", nil, err
	}
	if err := c.checkWorkflows(pr, p); err != nil {
		return "", nil, err
	}
	changed = changed || p.Repos != nil && !reflect.DeepEqual(*p.Repos, pr.Repos) || p.Links != nil && !reflect.DeepEqual(*p.Links, pr.Links) ||
		p.Context != nil && *p.Context != pr.Context || p.Defaults != nil && !reflect.DeepEqual(*p.Defaults, pr.Defaults) ||
		p.Hooks != nil && !reflect.DeepEqual(*p.Hooks, pr.Hooks) || p.Fetch != nil && !slices.Equal(*p.Fetch, pr.Fetch) ||
		p.Workflows != nil && !reflect.DeepEqual(*p.Workflows, pr.Workflows)
	if !changed {
		return pr.ID, nil, nil
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EProjectEdited, p)}, nil
}

func (c *Coord) projectMember(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.MemberSet
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	pr, err := c.managedProject(who, p.Project)
	if err != nil {
		return "", nil, err
	}
	if !slices.Contains([]string{"", task.RoleParticipant, task.RoleReader}, p.Role) {
		return "", nil, bad("role " + p.Role)
	}
	if p.Role != "" {
		if err := c.checkUser(p.User); err != nil {
			return "", nil, err
		}
	}
	if pr.Members[p.User] == p.Role {
		return pr.ID, nil, nil
	}
	return pr.ID, []journal.Event{journal.NewEvent(task.EMemberSet, p)}, nil
}

// machineShare replaces who else may dispatch to a machine: its owner or an admin decides.
func (c *Coord) machineShare(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.Share
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	p.Sessions = nil // machine.sessions sets it
	if c.ms[p.Machine] == nil || !c.canSee(who, p.Machine) {
		return "", nil, notFound("machine " + p.Machine)
	}
	if !who.Admin && c.ownerOf(p.Machine) != who.User {
		return "", nil, forbidden("machine " + p.Machine)
	}
	slices.Sort(p.Users)
	p.Users = slices.Compact(p.Users)
	slices.Sort(p.Projects)
	p.Projects = slices.Compact(p.Projects)
	for _, u := range p.Users {
		if err := c.checkUser(u); err != nil {
			return "", nil, err
		}
	}
	for _, id := range p.Projects {
		if c.st.Projects[id] == nil {
			return "", nil, notFound("project " + id)
		}
	}
	now := c.st.Shares[p.Machine]
	if now == nil {
		now = &task.Share{Machine: p.Machine}
	}
	if slices.Equal(now.Users, p.Users) && slices.Equal(now.Projects, p.Projects) && now.Approve == p.Approve {
		return p.Machine, nil, nil
	}
	return p.Machine, []journal.Event{journal.NewEvent(task.EMachineShared, p)}, nil
}

// machineSessions is machine.sessions: its owner alone says who else reads the machine's sessions; an admin does not
// for someone else.
func (c *Coord) machineSessions(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.SessionsSet
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if c.ms[p.Machine] == nil || !c.canSee(who, p.Machine) {
		return "", nil, notFound("machine " + p.Machine)
	}
	if c.ownerOf(p.Machine) != who.User {
		return "", nil, forbidden("machine " + p.Machine)
	}
	slices.Sort(p.Users)
	p.Users = slices.Compact(p.Users)
	slices.Sort(p.Projects)
	p.Projects = slices.Compact(p.Projects)
	for _, u := range p.Users {
		if err := c.checkUser(u); err != nil {
			return "", nil, err
		}
	}
	for _, id := range p.Projects {
		if c.st.Projects[id] == nil {
			return "", nil, notFound("project " + id)
		}
	}
	now := sessionView(c.st, p.Machine).(*task.SessionsSet)
	if slices.Equal(now.Users, p.Users) && slices.Equal(now.Projects, p.Projects) && now.Team == p.Team {
		return p.Machine, nil, nil
	}
	return p.Machine, []journal.Event{journal.NewEvent(task.ESessionsShared, p)}, nil
}

// machineDrain is machine.drain: its owner or an admin stops the machine taking new runs, or lets it take them again.
func (c *Coord) machineDrain(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.DrainSet
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if c.ms[p.Machine] == nil || !c.canSee(who, p.Machine) {
		return "", nil, notFound("machine " + p.Machine)
	}
	if !who.Admin && c.ownerOf(p.Machine) != who.User {
		return "", nil, forbidden("machine " + p.Machine)
	}
	if (c.st.Drains[p.Machine] != nil) == p.On {
		return p.Machine, nil, nil
	}
	p.By = ""
	if p.On {
		p.By = who.User
	}
	return p.Machine, []journal.Event{journal.NewEvent(task.EMachineDrained, p)}, nil
}

// drainView answers machine.drain: the drain, or only the machine when it takes runs.
func drainView(st *task.State, machine string) any {
	if d := st.Drains[machine]; d != nil {
		cp := *d
		return &cp
	}
	return &task.Drain{Machine: machine}
}

// runMessages is a page of run's conversation, for whoever may read the run: its session, not any of the machine's.
func (c *Coord) runMessages(ctx context.Context, who Principal, r *wire.Request) (any, error) {
	var p RunMessages
	if err := r.Decode(&p); err != nil {
		return nil, err
	}
	run, err := c.readableRun(who, p.Run)
	if err != nil {
		return nil, err
	}
	if run.Session == "" {
		return nil, notFound("session of " + run.ID)
	}
	var out json.RawMessage
	err = c.call(ctx, run.Machine, remote.MMessages, remote.MessagesParams{Ref: remote.Ref{Provider: run.Provider, SessionID: run.Session},
		Before: p.Before, N: p.N, File: p.File}, &out)
	return out, err
}

// Hooks a project may set.
var hookNames = []string{"setup", "before_run", "check", "cleanup"}

// checkSettings checks what a project edit sets besides its name and owner. The caller holds mu.
func (c *Coord) checkSettings(p task.ProjectEdit) error {
	argv := func(what string, a []string) error {
		if len(a) > 64 || slices.ContainsFunc(a, func(s string) bool { return len(s) > maxLine }) {
			return bad(what)
		}
		return nil
	}
	if p.Repos != nil {
		if len(*p.Repos) > 20 {
			return bad("repos")
		}
		for i, r := range *p.Repos {
			switch {
			case r.Name == "" || len(r.Name) > 64 || slices.ContainsFunc((*p.Repos)[:i], func(x task.Repo) bool { return x.Name == r.Name }):
				return bad("repo name " + r.Name)
			case len(r.Remote) > maxLine || len(r.Base) > 255:
				return bad("repo " + r.Name)
			}
			for m, d := range r.Dirs {
				if m == "" || len(m) > 64 || d == "" || len(d) > 4096 {
					return bad("repo " + r.Name + " dir on " + m)
				}
			}
		}
	}
	if p.Links != nil && (len(*p.Links) > 50 || slices.ContainsFunc(*p.Links, func(l task.Link) bool {
		return l.Kind == "" || len(l.Kind) > 32 || l.URL == "" || len(l.URL) > maxLine
	})) {
		return bad("links")
	}
	if p.Context != nil && len(*p.Context) > maxBrief {
		return bad("context")
	}
	if d := p.Defaults; d != nil {
		if err := c.checkMachine(d.Machine); err != nil {
			return err
		}
		for _, a := range append([]string{d.Agent}, slices.Collect(maps.Values(d.Roles))...) {
			if err := c.checkAgent(a); err != nil {
				return err
			}
		}
	}
	if p.Hooks != nil {
		for k, a := range *p.Hooks {
			if !slices.Contains(hookNames, k) || len(a) == 0 {
				return bad("hook " + k)
			}
			if err := argv("hook "+k, a); err != nil {
				return err
			}
		}
	}
	if p.Fetch != nil {
		return argv("fetch", *p.Fetch)
	}
	return nil
}

// Offboard is user.offboard and user.offboard.preview: hand what user holds to others before they leave. To takes what
// no project owner does; "" is the caller.
type Offboard struct {
	User string `json:"user"`
	To   string `json:"to,omitempty"`
}

// OffboardPlan answers user.offboard.preview: what user.offboard would do now.
type OffboardPlan struct {
	User     string         `json:"user"`
	To       string         `json:"to"`
	Projects []string       `json:"projects,omitempty"` // the projects they own, to To; their personal projects stay theirs
	Left     []string       `json:"left,omitempty"`     // the projects they leave
	Tasks    []OffboardTask `json:"tasks,omitempty"`    // their unfinished tasks, as owner or approver, each to its heir
	Defs     []string       `json:"defs,omitempty"`     // their agent definitions, to To
	Machines []string       `json:"machines,omitempty"` // their machines, closed to everyone else, their sessions included
	Canceled int            `json:"canceled,omitempty"` // runs others queued on those machines, canceled before they start
	Private  int            `json:"private,omitempty"`  // their unfinished private tasks, those in their personal projects included: kept, their runs stopped
}

// OffboardTask is a task that changes hands, and whose it becomes.
type OffboardTask struct {
	ID string `json:"id"`
	To string `json:"to"`
}

// userOffboard does what offboard plans.
func (c *Coord) userOffboard(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p Offboard
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	_, events, err := c.offboard(who, p)
	return p.User, events, err
}

func (c *Coord) offboardPreview(who Principal, r *wire.Request) (OffboardPlan, error) {
	var p Offboard
	if err := r.Decode(&p); err != nil {
		return OffboardPlan{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	plan, _, err := c.offboard(who, p)
	return plan, err
}

// offboard hands user's projects, tasks and definitions on, takes them out of every project and closes their machines
// to everyone else, sessions included; runs queued there by others are then canceled before they start. Their private
// tasks (outside any project, or in a personal project) and personal projects stay theirs, and those tasks' runs stop.
// The caller holds mu.
func (c *Coord) offboard(who Principal, p Offboard) (OffboardPlan, []journal.Event, error) {
	if _, ok := c.user(p.User); !ok || p.User == Owner.User {
		return OffboardPlan{}, nil, notFound("user " + p.User)
	}
	to := cmp.Or(p.To, who.User)
	if to == p.User {
		return OffboardPlan{}, nil, bad("to")
	}
	if err := c.checkUser(to); err != nil {
		return OffboardPlan{}, nil, err
	}
	plan := OffboardPlan{User: p.User, To: to}
	var events []journal.Event
	owners := map[string]string{} // project → its owner once this is done
	for _, id := range slices.Sorted(maps.Keys(c.st.Projects)) {
		pr := c.st.Projects[id]
		owners[id] = pr.Owner
		if pr.Owner == p.User && !pr.Personal() {
			owners[id] = to
			plan.Projects = append(plan.Projects, id)
			events = append(events, journal.NewEvent(task.EProjectEdited, task.ProjectEdit{ID: id, Owner: &to}))
		}
		if _, member := pr.Members[p.User]; member {
			plan.Left = append(plan.Left, id)
			events = append(events, journal.NewEvent(task.EMemberSet, task.MemberSet{Project: id, User: p.User}))
		}
	}
	private := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(c.st.Tasks)) {
		t := c.st.Tasks[id]
		if task.Finished(t.Status) || t.Owner != p.User && t.Approver != p.User {
			continue
		}
		if t.Project == "" || c.st.Projects[t.Project].Personal() {
			private[id] = t.Owner == p.User
			continue
		}
		heir := cmp.Or(owners[t.Project], to)
		plan.Tasks = append(plan.Tasks, OffboardTask{ID: id, To: heir})
		e := task.TaskEdit{ID: id}
		if t.Owner == p.User {
			e.Owner = &heir
		}
		if t.Approver == p.User {
			e.Approver = &heir
		}
		events = append(events, journal.NewEvent(task.ETaskEdited, e))
	}
	for _, kept := range private {
		if kept {
			plan.Private++
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.st.Runs)) {
		r := c.st.Runs[id]
		switch {
		case !private[r.Task]:
		case r.State == task.Queued:
			events = append(events, journal.NewEvent(task.ERunCanceled, task.RunRef{ID: id, Reason: ReasonAccessRevoked}))
		case task.Open(r.State) && r.Want != "stop":
			events = append(events, journal.NewEvent(task.ERunStopAsked, task.RunRef{ID: id}))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.st.AgentDefs)) {
		if d := c.st.AgentDefs[name]; d.Owner == p.User {
			cp := *d
			cp.Owner = to
			plan.Defs = append(plan.Defs, name)
			events = append(events, journal.NewEvent(task.EAgentDefSaved, cp))
		}
	}
	for _, m := range slices.Sorted(maps.Keys(c.ms)) {
		if c.ownerOf(m) == p.User && !c.retired(m) {
			plan.Machines = append(plan.Machines, m)
		}
	}
	for _, r := range c.st.Runs {
		if r.State == task.Queued && r.Dispatcher != p.User && slices.Contains(plan.Machines, r.Machine) {
			plan.Canceled++
		}
	}
	for _, m := range slices.Sorted(maps.Keys(c.st.Shares)) {
		s := c.st.Shares[m]
		if c.ownerOf(m) != p.User {
			continue
		}
		if len(s.Users) > 0 || len(s.Projects) > 0 || s.Approve {
			events = append(events, journal.NewEvent(task.EMachineShared, task.Share{Machine: m}))
		}
		if s.Sessions != nil {
			events = append(events, journal.NewEvent(task.ESessionsShared, task.SessionsSet{Machine: m}))
		}
	}
	return plan, events, nil
}

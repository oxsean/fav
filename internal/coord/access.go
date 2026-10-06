package coord

import (
	"encoding/json"
	"slices"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// Principal is who calls the coordinator: every connection carries one, and every method checks it.
type Principal struct {
	User   string `json:"user"`
	Admin  bool   `json:"admin,omitempty"`
	system bool
}

// Owner is this machine's user: the socket, a process that became the coordinator, and mode 1. It owns every machine
// of mode 1.
var Owner = Principal{User: "local", Admin: true}

// User is a person of the team, as mode 2 knows them.
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Admin    bool   `json:"admin,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// ReasonAccessRevoked: a queued run's dispatcher may no longer run it there (run_canceled).
const ReasonAccessRevoked = "access_revoked"

func (p Principal) actor() journal.Actor {
	if p.system {
		return journal.System
	}
	return journal.Actor{Kind: journal.ActorUser, ID: p.User}
}

// access is what a method needs from its caller before the method's own checks.
type access int

const (
	anyone   access = iota + 1 // the handshake
	reader                     // reads what it may see
	writer                     // changes what it may change
	admin                      // an instance administrator
	internal                   // tend-server's own work (System)
)

// methodAccess covers every method a client may call; one missing here is refused.
var methodAccess = map[string]access{
	wire.MPing:           anyone,
	remote.MHello:        anyone,
	MStateGet:            reader,
	MTaskGet:             reader,
	MAgentList:           reader,
	MMachineList:         reader,
	MMachineCheck:        reader, // whoever sees the machine (checkMachines)
	MStateWatch:          reader,
	MRunTail:             reader,
	MRunOutputPage:       reader,
	MRunOutputWatch:      reader,
	MRunMessages:         reader,
	MRunOutputItem:       reader,
	MRunOutputFind:       reader,
	MRunChanges:          reader, // a directory run's other changes: its machine's owner only (runChanges)
	MRunDiff:             reader,
	MRunBlob:             reader,
	MProjectDirs:         reader, // who may run the project's tasks on the machine (projectDirs)
	MRunPreview:          reader,
	MNodeCall:            reader, // a machine's own sessions: its owner and whom its scope names (readsSessions); node.dirs: its owner and admins
	MSessionsList:        reader, // the same as node.call's session reads (readsSessions)
	MTaskCreate:          writer,
	MTaskEdit:            writer,
	MTaskStatus:          writer,
	MTaskUndo:            writer,
	MTaskStart:           writer,
	MTaskPause:           writer,
	MTaskMove:            writer,
	MAgentDefList:        reader,
	MAgentDefGet:         reader,
	MAgentDefSave:        writer,
	MAgentDefCheck:       reader, // agentdef.save's rules, saving nothing (agentDefCheck)
	MAgentDefRemove:      writer,
	MAgentDefShare:       writer,
	MAgentDefLeave:       writer,
	MAgentDefTransfer:    writer,
	MInboxList:           reader,
	MMachinesWatch:       reader,
	MInboxWatch:          reader,
	MUserOffboard:        admin,
	MUserOffboardPreview: admin,
	MTaskSync:            internal,
	MTaskLink:            internal,
	MTaskSourceAck:       writer,
	MTaskGate:            writer,
	MTaskMerge:           writer,
	MTaskPlan:            writer,
	MTaskPlanSave:        writer,
	MTaskPlanApply:       writer,
	MTaskMessage:         writer,
	MTaskMessagePreview:  reader,
	MRunDispatch:         writer,
	MRunStop:             writer,
	MRunAbandon:          writer,
	MRunAnswer:           writer,
	MRunSend:             writer,
	MRunInterrupt:        writer,
	MRunContinue:         writer, // any session of a machine: its owner only (runContinue)
	MProjectCreate:       writer, // a project of one's own; only an admin names another owner (projectCreate)
	MProjectAttach:       writer, // its owner and admins on any machine, a participant on their own (attachable)
	MProjectDetach:       writer,
	MProjectEdit:         writer,
	MProjectMember:       writer,
	MMachineShare:        writer,
	MMachineDrain:        writer, // its owner or an admin (machineDrain)
	MMachineSessions:     writer, // its owner only (machineSessions)
}

func forbidden(what string) error { return &wire.Error{Code: wire.CodeUnauthorized, Detail: what} }

// may is nil when p can call method at all.
func (p Principal) may(method string) error {
	a, ok := methodAccess[method]
	switch {
	case !ok:
		return &wire.Error{Code: wire.CodeUnknownMethod, Detail: method}
	case a == anyone:
		return nil
	case p.User == "":
		return forbidden(method)
	case a == admin && !p.Admin, a == internal && !p.system:
		return forbidden(method)
	}
	return nil
}

// team: mode 2's rules apply (machines have owners, users are known); mode 1 has one user who owns everything.
func (c *Coord) team() bool { return c.opt.MachineOwner != nil }

// ownerOf is the user who owns machine; "" nobody.
func (c *Coord) ownerOf(machine string) string {
	if c.opt.MachineOwner == nil {
		return Owner.User
	}
	return c.opt.MachineOwner(machine)
}

// retired: machine's owner was disabled (offboarded); the caller holds mu.
func (c *Coord) retired(machine string) bool {
	owner := c.ownerOf(machine)
	u, ok := c.user(owner)
	return c.team() && owner != Owner.User && ok && u.Disabled
}

// user is id as the team knows them; mode 1 knows only its owner.
func (c *Coord) user(id string) (User, bool) {
	if c.opt.Users == nil {
		return User{ID: id, Admin: id == Owner.User}, id == Owner.User
	}
	return c.opt.Users(id)
}

// person is user id as a node needs them to author a run's commits; nil for mode 1's owner or an unknown user.
func (c *Coord) person(id string) *node.Person {
	if c.opt.Users == nil || id == "" {
		return nil
	}
	u, ok := c.opt.Users(id)
	if !ok {
		return nil
	}
	return &node.Person{ID: u.ID, Name: u.Name, Email: u.Email}
}

// seesAll: p is above anyone's privacy: mode 1's owner, or tend-server's own work (System), which acts for whoever
// set it up.
func (c *Coord) seesAll(p Principal) bool { return p.Admin && !c.team() || p.system }

// roleIn is p's role in project: an admin participates in every project but someone else's personal one.
func (c *Coord) roleIn(st *task.State, p Principal, project string) string {
	pr := st.Projects[project]
	if p.Admin && (c.seesAll(p) || !pr.Personal()) {
		return task.RoleParticipant
	}
	return pr.Role(p.User)
}

// taskRole is p's role for t: its project's, or, for a task outside any project, its owner's alone (seesAll has every
// task, those without an owner included).
func (c *Coord) taskRole(st *task.State, p Principal, t *task.Task) string {
	switch {
	case t == nil:
		return ""
	case t.Project == "" && !c.seesAll(p):
		if p.User != "" && t.Owner == p.User {
			return task.RoleParticipant
		}
		return ""
	}
	return c.roleIn(st, p, t.Project)
}

func (c *Coord) canRead(st *task.State, p Principal, t *task.Task) bool {
	return c.taskRole(st, p, t) != ""
}

func (c *Coord) canWrite(st *task.State, p Principal, t *task.Task) bool {
	return c.taskRole(st, p, t) == task.RoleParticipant
}

// runTask is run's task, nil for a run the state does not hold.
func runTask(st *task.State, run *task.Run) *task.Task {
	if run == nil {
		return nil
	}
	return st.Tasks[run.Task]
}

// seesProject: p may know project pr exists: its members, and admins unless it is someone else's personal project.
func (c *Coord) seesProject(p Principal, pr *task.Project) bool {
	return pr.Role(p.User) != "" || p.Admin && (c.seesAll(p) || !pr.Personal())
}

// readsSessions: p may read machine's own sessions, through node.call or sessions.list: its owner, or whom its session
// scope names. Admins are not exempt, nor are machines under local. The caller holds mu.
func (c *Coord) readsSessions(p Principal, machine string) bool {
	return c.ownerOf(machine) == p.User || c.st.Shares[machine] != nil && c.st.Shares[machine].Sessions.Opens(c.st.Projects, p.User)
}

// canUse: p may start a run of project on machine: its owner, or someone it is shared with. Admins are not exempt,
// since a run uses the owner's logins and files.
func (c *Coord) canUse(p Principal, machine, project string) bool {
	return c.ownerOf(machine) == p.User || c.st.Shares[machine].Opens(p.User, project)
}

// canSee: p may know machine exists and how it stands; the caller holds mu.
func (c *Coord) canSee(p Principal, machine string) bool {
	if p.Admin || c.ownerOf(machine) == p.User {
		return true
	}
	s := c.st.Shares[machine]
	if s == nil {
		return false
	}
	if slices.Contains(s.Users, p.User) || s.Sessions.Opens(c.st.Projects, p.User) {
		return true
	}
	for _, id := range s.Projects {
		if c.st.Projects[id].Role(p.User) != "" {
			return true
		}
	}
	return false
}

// canApprove: p may grant or deny run's permission requests: the machine's owner, the run's dispatcher, or whom
// the machine is shared with when the share says so.
func (c *Coord) canApprove(p Principal, run *task.Run) bool {
	if c.ownerOf(run.Machine) == p.User || run.Dispatcher != "" && run.Dispatcher == p.User {
		return true
	}
	s := c.st.Shares[run.Machine]
	return s != nil && s.Approve && s.Opens(p.User, run.Project)
}

// stillAllowed: run's dispatcher may still start it where it is queued; the caller holds mu. Runs queued before
// dispatchers were kept, and mode 1's, are not checked.
func (c *Coord) stillAllowed(run *task.Run) bool {
	if !c.team() || run.Dispatcher == "" {
		return true
	}
	u, ok := c.user(run.Dispatcher)
	if !ok || u.Disabled {
		return false
	}
	p := Principal{User: u.ID, Admin: u.Admin}
	t := c.st.Tasks[run.Task]
	return c.canWrite(c.st, p, t) && c.canUse(p, run.Machine, t.Project)
}

// visibleState is st cut down to what p may see: st is a copy the caller owns.
func (c *Coord) visibleState(p Principal, st *task.State) *task.State {
	if p.Admin && !c.team() {
		return st
	}
	for id, t := range st.Tasks {
		if !c.canRead(st, p, t) {
			delete(st.Tasks, id)
		}
	}
	for id, r := range st.Runs {
		if st.Tasks[r.Task] == nil {
			delete(st.Runs, id)
		}
	}
	for id, pr := range st.Projects {
		if !c.seesProject(p, pr) {
			delete(st.Projects, id)
		}
	}
	c.mu.Lock()
	for m, s := range st.Shares {
		switch {
		case !c.canSee(p, m):
			delete(st.Shares, m)
		case s.Sessions != nil && c.ownerOf(m) != p.User: // the scope is its owner's to know
			cp := *s
			cp.Sessions = nil
			st.Shares[m] = &cp
		}
	}
	for m := range st.Drains {
		if !c.canSee(p, m) {
			delete(st.Drains, m)
		}
	}
	for name, d := range st.AgentDefs {
		if !c.readsDef(p, d) {
			delete(st.AgentDefs, name)
		}
	}
	for _, r := range st.Runs {
		c.cutDef(p, r)
	}
	c.mu.Unlock()
	return st
}

// subject is what an event is about: the fields events name it by.
type subject struct {
	ID      string `json:"id"`
	Task    string `json:"task"`
	Project string `json:"project"`
	Machine string `json:"machine"`
	Name    string `json:"name"`
	Parent  string `json:"parent"`
}

// sees: p may see event e, by what it is about now; the caller holds mu.
func (c *Coord) sees(p Principal, e journal.Event) bool {
	if p.Admin && !c.team() {
		return true
	}
	var s subject
	json.Unmarshal(e.Data, &s)
	switch e.Type {
	case task.ETaskCreated, task.ETaskEdited, task.ETaskStatus, task.ETaskRestored:
		return c.canRead(c.st, p, c.st.Tasks[s.ID])
	case task.ERunQueued, task.ERunStarting, task.ERunObserved, task.ERunStopAsked, task.ERunCanceled, task.ERunAbandoned,
		task.ERunAnswered, task.ERunSent, task.ERunInterrupt:
		return c.canRead(c.st, p, runTask(c.st, c.st.Runs[s.ID]))
	case task.EProjectCreated, task.EProjectEdited:
		return c.seesProject(p, c.st.Projects[s.ID])
	case task.EMemberSet:
		return c.seesProject(p, c.st.Projects[s.Project])
	case task.EMachineShared, task.EMachineDrained, task.ESessionsShared:
		return c.canSee(p, s.Machine)
	case task.ETaskMoved, task.ETaskHeld, task.ETaskPaused, task.ETaskSourced, task.ETaskSourceAcked, task.ETaskStaged, task.ETaskNoted,
		task.ETaskLinked, task.EPlanDrafted, task.EPlanApplied:
		return c.canRead(c.st, p, c.st.Tasks[s.ID])
	case task.ETaskStarted:
		var d task.TaskStart
		json.Unmarshal(e.Data, &d)
		return slices.ContainsFunc(d.IDs, func(id string) bool { return c.canRead(c.st, p, c.st.Tasks[id]) })
	case task.EAgentDefSaved, task.EAgentDefShared, task.EAgentDefTransferred:
		d := c.st.AgentDefs[s.Name]
		return d != nil && c.readsDef(p, d)
	case task.EAgentDefRemoved:
		return p.Admin
	}
	return p.Admin
}

// visibleEnv is env with only the events p may see: every seq still reaches p, empty when nothing in it is theirs.
// The command's result and digest stay with its caller, and its name with those who see some of what it did. The
// caller holds mu.
func (c *Coord) visibleEnv(p Principal, env journal.Envelope) journal.Envelope {
	var events []journal.Event
	for _, e := range env.Events {
		if c.sees(p, e) {
			events = append(events, c.eventFor(p, e))
		}
	}
	if env.Command != nil {
		env.Command = &journal.Receipt{ID: env.Command.ID, Method: env.Command.Method}
		if len(events) == 0 && env.Who().ID != p.User {
			env.Command = nil
		}
	}
	env.Events = events
	return env
}

// reshapes: env changes who sees what, so a subscriber's copy of the state may no longer match what it may see.
func reshapes(env journal.Envelope) bool {
	for _, e := range env.Events {
		switch e.Type {
		case task.EMemberSet, task.EProjectEdited, task.EMachineShared, task.ESessionsShared, task.EAgentDefShared, task.EAgentDefRemoved, task.EAgentDefTransferred:
			return true
		case task.ETaskEdited:
			var d task.TaskEdit
			if json.Unmarshal(e.Data, &d) == nil && (d.Project != nil || d.Owner != nil) {
				return true
			}
		}
	}
	return false
}

// seesResult: p may still read what a receipt's result is about; the caller holds mu.
func (c *Coord) seesResult(p Principal, result json.RawMessage) bool {
	var s subject
	if json.Unmarshal(result, &s) != nil || s.ID == "" {
		return true
	}
	if t := c.st.Tasks[s.ID]; t != nil {
		return c.canRead(c.st, p, t)
	}
	if r := c.st.Runs[s.ID]; r != nil {
		return c.canRead(c.st, p, runTask(c.st, r))
	}
	if pr := c.st.Projects[s.ID]; pr != nil {
		return c.seesProject(p, pr)
	}
	return true
}

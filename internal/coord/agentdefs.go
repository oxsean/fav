package coord

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// AgentDefSave is agentdef.save: a definition's Markdown. Owner "project:<id>" gives a new one to a project.
type AgentDefSave struct {
	Text  string `json:"text"`
	Owner string `json:"owner,omitempty"`
}

// AgentDefView is a definition as the caller may see it: its text only when they may read it.
type AgentDefView struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Role        string         `json:"role,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	Model       string         `json:"model,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	Owner       string         `json:"owner"`
	Share       *task.DefShare `json:"share,omitempty"` // to whoever manages it
	Manage      bool           `json:"manage,omitempty"`
	Text        string         `json:"text,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
	Rev         int            `json:"rev,omitzero"`
	UpdatedAt   time.Time      `json:"updated_at,omitzero"`
	Launch      []string       `json:"launch,omitempty"` // what a run of it starts, to whoever may read it
}

type AgentDefList struct {
	Defs []AgentDefView `json:"defs"`
}

// defsDir is where mode 1 keeps its definitions, one Markdown file each.
func (c *Coord) defsDir() string { return filepath.Join(c.opt.Home, "defs", "agents") }

// agentDefs are the definitions: a team's journal, mode 1's files (owned by this machine's user). The caller holds mu.
func (c *Coord) agentDefs() map[string]*task.AgentDef {
	if c.team() {
		return c.st.AgentDefs
	}
	out := map[string]*task.AgentDef{}
	files, _ := filepath.Glob(filepath.Join(c.defsDir(), "*.md"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		d, err := defs.Parse(b)
		if err != nil || d.Name+".md" != filepath.Base(f) {
			continue
		}
		out[d.Name] = &task.AgentDef{AgentDef: d, Owner: Owner.User}
	}
	return out
}

// manages: p may change or share d: its owner, the owner of the project that owns it, an admin. The caller holds mu.
func (c *Coord) manages(p Principal, d *task.AgentDef) bool {
	if p.Admin || d.Owner == p.User {
		return true
	}
	pr := c.st.Projects[d.Project()]
	return pr != nil && pr.Owner == p.User
}

// usesDef: p may run d for a task of project. The caller holds mu.
func (c *Coord) usesDef(p Principal, d *task.AgentDef, project string) bool {
	return !c.team() || d.Usable(p.User, project, roleIn(c.st, p, project))
}

// usesDefSomewhere: p may run d for some task. The caller holds mu.
func (c *Coord) usesDefSomewhere(p Principal, d *task.AgentDef) bool {
	if c.usesDef(p, d, "") {
		return true
	}
	for id := range c.st.Projects {
		if c.usesDef(p, d, id) {
			return true
		}
	}
	return false
}

// readsDef: p may read d's instructions. The caller holds mu.
func (c *Coord) readsDef(p Principal, d *task.AgentDef) bool {
	return c.manages(p, d) || d.Share.View && c.usesDefSomewhere(p, d)
}

func (c *Coord) defView(p Principal, d *task.AgentDef) AgentDefView {
	v := AgentDefView{Name: d.Name, Description: d.Description, Role: d.Role, Provider: d.Provider, Model: d.Model, Effort: d.Effort,
		Owner: d.Owner, Rev: d.Rev, UpdatedAt: d.UpdatedAt}
	if c.manages(p, d) {
		share := d.Share
		v.Share, v.Manage = &share, true
	}
	if c.readsDef(p, d) {
		v.Text = string(defs.Format(d.AgentDef))
		_, v.Warnings = defs.Check(d.AgentDef)
		v.Launch = c.launchOf(d)
	}
	return v
}

// launchOf is the command a headless run of d starts, with placeholders for the run's directory and brief.
func (c *Coord) launchOf(d *task.AgentDef) []string {
	prof, err := defs.Compile(d.AgentDef, c.profile)
	if err != nil {
		return nil
	}
	cs, err := agent.LaunchOf(agent.LaunchSpec{Profile: prof, Dir: "<dir>", PromptFile: "<brief>", Headless: true, Stream: true})
	if err != nil {
		return nil
	}
	cs.Exec = filepath.Base(cs.Exec)
	return cs.Argv()
}

func (c *Coord) agentDefList(p Principal) AgentDefList {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := AgentDefList{Defs: []AgentDefView{}}
	for _, d := range c.agentDefs() {
		if c.manages(p, d) || c.usesDefSomewhere(p, d) {
			out.Defs = append(out.Defs, c.defView(p, d))
		}
	}
	sort.Slice(out.Defs, func(i, j int) bool { return out.Defs[i].Name < out.Defs[j].Name })
	return out
}

func (c *Coord) agentDefGet(p Principal, name string) (AgentDefView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.agentDefs()[name]
	if d == nil || !c.manages(p, d) && !c.usesDefSomewhere(p, d) {
		return AgentDefView{}, notFound("agent " + name)
	}
	return c.defView(p, d), nil
}

// managedDef is definition name when who may change it: not found when they may not even see it. The caller holds mu.
func (c *Coord) managedDef(who Principal, name string) (*task.AgentDef, error) {
	d := c.agentDefs()[name]
	switch {
	case d == nil || !c.manages(who, d) && !c.usesDefSomewhere(who, d):
		return nil, notFound("agent " + name)
	case !c.manages(who, d):
		return nil, forbidden("agent " + name)
	}
	return d, nil
}

func (c *Coord) agentDefSave(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p AgentDefSave
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	d, err := defs.Parse([]byte(p.Text))
	if err != nil {
		return "", nil, bad(err.Error())
	}
	if errs, _ := defs.Check(d); len(errs) > 0 {
		return "", nil, bad(strings.Join(errs, "; "))
	}
	if _, err := defs.Compile(d, c.profile); err != nil {
		return "", nil, bad(err.Error())
	}
	rec := task.AgentDef{AgentDef: d, Owner: who.User}
	if old := c.agentDefs()[d.Name]; old != nil {
		if _, err := c.managedDef(who, d.Name); err != nil {
			return "", nil, err
		}
		rec.Owner, rec.Share = old.Owner, old.Share
	} else if p.Owner != "" && p.Owner != who.User {
		pr := c.st.Projects[strings.TrimPrefix(p.Owner, task.ProjectOwner)]
		switch {
		case !strings.HasPrefix(p.Owner, task.ProjectOwner) || pr == nil || !who.Admin && pr.Role(who.User) == "":
			return "", nil, notFound(p.Owner)
		case !who.Admin && pr.Owner != who.User:
			return "", nil, forbidden(p.Owner)
		}
		rec.Owner = p.Owner
	}
	if !c.team() {
		if err := fileio.WriteFile(filepath.Join(c.defsDir(), d.Name+".md"), defs.Format(d), 0o600); err != nil {
			return "", nil, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
		}
		return d.Name, nil, nil
	}
	return d.Name, []journal.Event{journal.NewEvent(task.EAgentDefSaved, rec)}, nil
}

func (c *Coord) agentDefRemove(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.AgentDefRef
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	if _, err := c.managedDef(who, p.Name); err != nil {
		return "", nil, err
	}
	if !c.team() {
		if err := os.Remove(filepath.Join(c.defsDir(), p.Name+".md")); err != nil {
			return "", nil, &wire.Error{Code: wire.CodeInternal, Detail: err.Error()}
		}
		return p.Name, nil, nil
	}
	return p.Name, []journal.Event{journal.NewEvent(task.EAgentDefRemoved, p)}, nil
}

func (c *Coord) agentDefShare(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p task.AgentDefShare
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	d, err := c.managedDef(who, p.Name)
	if err != nil {
		return "", nil, err
	}
	if !c.team() {
		return "", nil, bad("sharing needs tend-server")
	}
	slices.Sort(p.Share.Users)
	p.Share.Users = slices.Compact(p.Share.Users)
	slices.Sort(p.Share.Projects)
	p.Share.Projects = slices.Compact(p.Share.Projects)
	for _, u := range p.Share.Users {
		if err := c.checkUser(u); err != nil {
			return "", nil, err
		}
	}
	for _, id := range p.Share.Projects {
		if c.st.Projects[id] == nil {
			return "", nil, notFound("project " + id)
		}
	}
	if p.Share.All {
		p.Share.Users, p.Share.Projects = nil, nil
	}
	if slices.Equal(p.Share.Users, d.Share.Users) && slices.Equal(p.Share.Projects, d.Share.Projects) &&
		p.Share.All == d.Share.All && p.Share.View == d.Share.View {
		return d.Name, nil, nil
	}
	return d.Name, []journal.Event{journal.NewEvent(task.EAgentDefShared, p)}, nil
}

// agentFor is the profile who's run of a task of project is frozen with, and what its definition tells the run. A
// definition of that name comes first, then a configured profile. The caller holds mu.
func (c *Coord) agentFor(who Principal, name, project string) (tend.AgentProfile, *task.AgentDef, error) {
	if d := c.agentDefs()[name]; d != nil {
		if !c.usesDef(who, d, project) {
			if c.manages(who, d) || c.usesDefSomewhere(who, d) {
				return tend.AgentProfile{}, nil, forbidden("agent " + name + " is not shared for this task")
			}
			return tend.AgentProfile{}, nil, notFound("agent " + name)
		}
		prof, err := defs.Compile(d.AgentDef, c.profile)
		if err != nil {
			return tend.AgentProfile{}, nil, bad("agent " + name + ": " + err.Error())
		}
		return prof, d, nil
	}
	prof, ok := c.profile(name)
	if !ok {
		return tend.AgentProfile{}, nil, notFound("agent " + name)
	}
	return prof, nil, nil
}

// usableProfiles are the agents p can pick: the configured profiles and the definitions they may run.
func (c *Coord) usableProfiles(p Principal) []tend.AgentProfile {
	out := c.Profiles()
	c.mu.Lock()
	defer c.mu.Unlock()
	var names []string
	ds := c.agentDefs()
	for name, d := range ds {
		if c.usesDefSomewhere(p, d) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		prof, err := defs.Compile(ds[name].AgentDef, c.profile)
		if err != nil {
			continue
		}
		if i := slices.IndexFunc(out, func(q tend.AgentProfile) bool { return q.Name == name }); i >= 0 {
			out[i] = prof
		} else {
			out = append(out, prof)
		}
	}
	return out
}

// defAnswer answers a definition command with the definition as p sees it.
func (c *Coord) defAnswer(p Principal) func(*task.State, string) any {
	return func(st *task.State, name string) any {
		d := st.AgentDefs[name]
		if d == nil {
			d = c.agentDefs()[name]
		}
		if d == nil {
			return nil
		}
		return c.defView(p, d)
	}
}

// preferred is the first machine d prefers that is connected and takes new runs, else the first that takes them,
// else the first it prefers. The caller holds mu.
func (c *Coord) preferred(d *task.AgentDef) string {
	if d == nil {
		return ""
	}
	for _, m := range d.Machines.Prefer {
		if x := c.ms[m]; x != nil && x.conn != nil && c.st.Drains[m] == nil {
			return m
		}
	}
	for _, m := range d.Machines.Prefer {
		if c.st.Drains[m] == nil {
			return m
		}
	}
	if len(d.Machines.Prefer) > 0 {
		return d.Machines.Prefer[0]
	}
	return ""
}

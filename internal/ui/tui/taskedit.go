package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// editing is a text $EDITOR has open: what it was, and how the edited text is saved.
type editing struct {
	key  string
	path string
	orig []byte
	save func(*Model, []byte) (tea.Cmd, error)
}

type editedMsg struct{ err error }

// editorCmd opens path in $VISUAL / $EDITOR, else in VS Code, waiting until it is closed.
func editorCmd(path string) *exec.Cmd {
	if parts := shell.Editor(); len(parts) > 0 {
		return exec.Command(parts[0], append(parts[1:], path)...)
	}
	if code, err := exec.LookPath("code"); err == nil {
		return exec.Command(code, "--wait", path)
	}
	return nil
}

// editInEditor opens text (or what an earlier try left unsaved under key) in the editor; save sends the result.
func (m *Model) editInEditor(key, ext string, text []byte, save func(*Model, []byte) (tea.Cmd, error)) tea.Cmd {
	if m.tasks.cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	start := text
	if b, ok := m.tasks.unsaved[key]; ok {
		start = b
	}
	f, err := os.CreateTemp("", "tend-edit-*"+ext)
	if err == nil {
		_, err = f.Write(start)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		m.flash(i18n.F("tasks.edit_failed", err.Error()))
		return nil
	}
	c := editorCmd(f.Name())
	if c == nil {
		os.Remove(f.Name())
		m.flash(i18n.T("cli.edit.no_editor"))
		return nil
	}
	m.tasks.editing = &editing{key: key, path: f.Name(), orig: text, save: save}
	return tea.ExecProcess(c, func(err error) tea.Msg { return editedMsg{err} })
}

func (msg editedMsg) apply(m *Model) tea.Cmd {
	e := m.tasks.editing
	m.tasks.editing = nil
	if e == nil {
		return nil
	}
	b, err := os.ReadFile(e.path)
	os.Remove(e.path)
	if msg.err != nil {
		err = msg.err
	}
	if err != nil {
		m.flash(i18n.F("tasks.edit_failed", err.Error()))
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(e.orig)) {
		delete(m.tasks.unsaved, e.key)
		m.flash(i18n.T("tasks.edit_unchanged"))
		return nil
	}
	if m.tasks.unsaved == nil {
		m.tasks.unsaved = map[string][]byte{}
	}
	m.tasks.unsaved[e.key] = b
	cmd, err := e.save(m, b)
	if err != nil {
		m.flash(i18n.F("tasks.edit_bad", err.Error()))
		return nil
	}
	return cmd
}

// saved clears what an edit left unsaved once the coordinator took it.
func saved(key string, more func(*Model)) func(*Model) tea.Cmd {
	return func(mm *Model) tea.Cmd {
		delete(mm.tasks.unsaved, key)
		if more != nil {
			more(mm)
		}
		return nil
	}
}

func (m *Model) planTask() tea.Cmd {
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	return m.write(coord.MTaskPlan, coord.TaskPlan{ID: x.ID}, i18n.F("tasks.planning", x.ID), nil)
}

func hasDraft(x *task.Task) bool { return x != nil && x.Draft != nil && x.Draft.Plan != nil }

func (m *Model) openDraft() {
	x := m.selectedTask()
	if !hasDraft(x) {
		return
	}
	m.ov = overlay{kind: ovTaskDraft, focus: -1, taskID: x.ID}
}

func (m *Model) draftButtons() []btn {
	x := m.selectedTask()
	if !hasDraft(x) {
		return []btn{cancelBtn()}
	}
	id, rev, n := x.ID, x.Rev, len(x.Draft.Plan.Tasks)
	return []btn{
		{keyed(enterKey, i18n.T("tasks.btn_apply_draft")), true, func(mm *Model) {
			mm.closeOverlay()
			mm.pending = mm.write(coord.MTaskPlanApply, coord.PlanApply{ID: id, ExpectedRev: rev}, i18n.F("tasks.draft_applied", n), nil)
		}},
		{keyed(keyOf(inList, actEdit), i18n.T("key.edit")), false, func(mm *Model) { mm.pending = mm.editDraft() }},
		{i18n.T("tasks.btn_discard"), false, func(mm *Model) {
			mm.openConfirm(i18n.F("tasks.draft_discard_title", id), i18n.T("tasks.btn_discard"), []string{i18n.T("tasks.draft_discard_body")},
				func(mm *Model) {
					mm.pending = mm.write(coord.MTaskPlanSave, coord.PlanSave{ID: id, ExpectedRev: rev}, i18n.T("tasks.draft_discarded"), nil)
				}, (*Model).openDraft)
		}},
		cancelBtn(),
	}
}

// editDraft opens the draft as JSON, the shape `tend task draft --save` reads.
func (m *Model) editDraft() tea.Cmd {
	x := m.selectedTask()
	if !hasDraft(x) {
		return nil
	}
	b, err := json.MarshalIndent(x.Draft.Plan, "", "  ")
	if err != nil {
		return nil
	}
	id, rev, key := x.ID, x.Rev, "plan:"+x.ID
	return m.editInEditor(key, ".json", append(b, '\n'), func(mm *Model, b []byte) (tea.Cmd, error) {
		p, err := task.ParsePlan(b)
		if err != nil {
			return nil, err
		}
		return mm.write(coord.MTaskPlanSave, coord.PlanSave{ID: id, Plan: p, ExpectedRev: rev}, i18n.T("tasks.draft_saved"), saved(key, nil)), nil
	})
}

// draftLines: the draft's subtasks, each under the one it is part of, then the planner's questions.
func draftLines(p *task.Plan, inner int) []string {
	var out []string
	line := func(pt task.PlanTask, depth int) {
		indent := strings.Repeat("  ", depth)
		out = append(out, indent+accent.Render("•")+" "+render.Truncate(pt.Title, inner-len(indent)-2))
		var meta []string
		for _, s := range []string{pt.Key, pt.Size, pt.Workflow, pt.Role} {
			if s != "" {
				meta = append(meta, s)
			}
		}
		if len(pt.After) > 0 {
			meta = append(meta, i18n.F("tasks.draft_after", strings.Join(pt.After, ", ")))
		}
		out = append(out, dimmed.Render(render.Truncate(indent+"  "+strings.Join(meta, " · "), inner)))
	}
	keys := map[string]bool{}
	for _, pt := range p.Tasks {
		keys[pt.Key] = true
	}
	for _, pt := range p.Tasks {
		if pt.Parent != "" && keys[pt.Parent] {
			continue
		}
		line(pt, 0)
		for _, kid := range p.Tasks {
			if kid.Parent == pt.Key {
				line(kid, 1)
			}
		}
	}
	if len(p.Questions) > 0 {
		out = append(out, "", accent.Render(i18n.F("tasks.draft_questions", len(p.Questions))))
		for _, q := range p.Questions {
			for i, l := range render.Wrap(q, inner-2) {
				if i == 0 {
					out = append(out, "? "+l)
				} else {
					out = append(out, "  "+l)
				}
			}
		}
	}
	return out
}

func (m *Model) renderDraft() string {
	w := m.ovWidth()
	inner := w - 4
	x := m.selectedTask()
	if !hasDraft(x) {
		return ovRender([]string{dimmed.Render(i18n.T("tasks.gone"))}, w)
	}
	p := x.Draft.Plan
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("tasks.draft_title", x.Title), inner)),
		dimmed.Render(i18n.F("tasks.draft_count", len(p.Tasks), len(p.Questions))), frame.Render(strings.Repeat(hRule, inner))}
	lines := draftLines(p, inner)
	end := m.scrollWindow(len(lines), max(3, m.h-14))
	body = append(body, lines[m.ov.cursor:end]...)
	if end < len(lines) {
		body = append(body, dimmed.Render(i18n.F("tasks.more_lines", len(lines)-end)))
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, m.draftButtons())...)
	return ovRender(body, w)
}

func (m *Model) draftKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if keyAct(inList, k) == actEdit {
		return m.editDraft()
	}
	if a := keyAct(inReader, k); a == actDown || a == actUp {
		m.scrollKey(a)
		return nil
	}
	m.dialogKey(keyAct(inConfirm, k), func() {
		for _, b := range m.draftButtons() {
			if b.primary {
				b.act(m)
				return
			}
		}
	})
	return nil
}

// projectText is what of a project its settings editor shows.
type projectText struct {
	Name      string              `json:"name"`
	Owner     string              `json:"owner,omitempty"`
	Repos     []task.Repo         `json:"repos"`
	Links     []task.Link         `json:"links"`
	Context   string              `json:"context"`
	Defaults  task.Defaults       `json:"defaults"`
	Hooks     map[string][]string `json:"hooks"`
	Fetch     []string            `json:"fetch"`
	Workflows map[string]string   `json:"workflows"`
}

func projectTextOf(pr *task.Project) projectText {
	t := projectText{Name: pr.Name, Owner: pr.Owner, Repos: pr.Repos, Links: pr.Links, Context: pr.Context, Defaults: pr.Defaults,
		Hooks: pr.Hooks, Fetch: pr.Fetch, Workflows: pr.Workflows}
	if t.Repos == nil {
		t.Repos = []task.Repo{}
	}
	if t.Links == nil {
		t.Links = []task.Link{}
	}
	if t.Hooks == nil {
		t.Hooks = map[string][]string{}
	}
	if t.Fetch == nil {
		t.Fetch = []string{}
	}
	if t.Workflows == nil {
		t.Workflows = map[string]string{}
	}
	return t
}

// projectEdit is the edit from before to after: only the fields that changed; nil when none did.
func projectEdit(id string, before, after projectText) *task.ProjectEdit {
	same := func(a, b any) bool {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Equal(x, y)
	}
	e := task.ProjectEdit{ID: id}
	changed := false
	set := func(a, b any, apply func()) {
		if !same(a, b) {
			apply()
			changed = true
		}
	}
	set(before.Name, after.Name, func() { e.Name = &after.Name })
	set(before.Owner, after.Owner, func() { e.Owner = &after.Owner })
	set(before.Repos, after.Repos, func() { e.Repos = &after.Repos })
	set(before.Links, after.Links, func() { e.Links = &after.Links })
	set(before.Context, after.Context, func() { e.Context = &after.Context })
	set(before.Defaults, after.Defaults, func() { e.Defaults = &after.Defaults })
	set(before.Hooks, after.Hooks, func() { e.Hooks = &after.Hooks })
	set(before.Fetch, after.Fetch, func() { e.Fetch = &after.Fetch })
	set(before.Workflows, after.Workflows, func() { e.Workflows = &after.Workflows })
	if !changed {
		return nil
	}
	return &e
}

func (m *Model) taskProject() *task.Project {
	if x := m.selectedTask(); x != nil && x.Project != "" {
		return m.tasks.st.Projects[x.Project]
	}
	return nil
}

// editProject opens the task's project's settings as JSON.
func (m *Model) editProject() tea.Cmd {
	pr := m.taskProject()
	if pr == nil {
		return nil
	}
	before := projectTextOf(pr)
	b, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return nil
	}
	id, name, key := pr.ID, pr.Name, "project:"+pr.ID
	return m.editInEditor(key, ".json", append(b, '\n'), func(mm *Model, b []byte) (tea.Cmd, error) {
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		var after projectText
		if err := d.Decode(&after); err != nil {
			return nil, err
		}
		e := projectEdit(id, before, after)
		if e == nil {
			delete(mm.tasks.unsaved, key)
			mm.flash(i18n.T("tasks.edit_unchanged"))
			return nil, nil
		}
		return mm.write(coord.MProjectEdit, e, i18n.F("tasks.project_saved", firstNonEmpty(after.Name, name)), saved(key, nil)), nil
	})
}

const agentTemplate = `---
name: my-agent
description: what it is for
role: implement
provider: claude
model: sonnet
effort: high
permission: acceptEdits
tools: {deny: [WebFetch]}
---
How it works, what it must not do.
`

type agentDefMsg struct {
	name string
	v    coord.AgentDefView
	err  error
}

// editAgent opens agent name's definition, when the coordinator has one this user may change.
func (m *Model) editAgent(name string) tea.Cmd {
	cl := m.tasks.cl
	if cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var v coord.AgentDefView
		err := cl.Call(ctx, coord.MAgentDefGet, task.AgentDefRef{Name: name}, &v)
		return agentDefMsg{name: name, v: v, err: err}
	}
}

func (msg agentDefMsg) apply(m *Model) tea.Cmd {
	var we *wire.Error
	switch {
	case errors.As(msg.err, &we) && we.Code == wire.CodeNotFound:
		m.flash(i18n.F("tasks.agent_not_def", msg.name))
		return nil
	case msg.err != nil:
		m.flash(i18n.F("tasks.failed", reasonText(msg.err)))
		return nil
	case !msg.v.Manage || msg.v.Text == "":
		m.flash(i18n.F("tasks.agent_not_yours", msg.name))
		return nil
	}
	return m.editInEditor("agent:"+msg.name, ".md", []byte(msg.v.Text), saveAgent("agent:"+msg.name))
}

func (m *Model) newAgent() tea.Cmd {
	return m.editInEditor("agent:", ".md", []byte(agentTemplate), saveAgent("agent:"))
}

// saveAgent checks a definition's Markdown here first, so a mistake is shown before the coordinator sees it.
func saveAgent(key string) func(*Model, []byte) (tea.Cmd, error) {
	return func(mm *Model, b []byte) (tea.Cmd, error) {
		d, err := defs.Parse(b)
		if err != nil {
			return nil, err
		}
		if errs, _ := defs.Check(d); len(errs) > 0 {
			return nil, errors.New(strings.Join(errs, "; "))
		}
		return mm.write(coord.MAgentDefSave, coord.AgentDefSave{Text: string(b)}, i18n.F("tasks.agent_saved", d.Name),
			saved(key, func(mm *Model) { mm.tasks.agents = nil })), nil
	}
}

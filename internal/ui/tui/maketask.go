package tui

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// The make-task form (「建成任务」 in the resume dialog, or the palette): a task whose run goes on with a session of this
// machine in the background (run.continue), private unless a project is picked. Keys as in the reply dialog: Enter
// starts a new line in the text, Ctrl+S makes the task, Tab walks the fields and the buttons, Esc discards.
const (
	makeText = iota
	makeAgent
	makeProject
	makeButtons
)

// makeTaskWhy is the i18n key of why r cannot become a task now, "" when it can.
func (m *Model) makeTaskWhy(r *tend.Rec) string {
	s := &m.proj.snap
	switch {
	case r == nil || r.Host != "":
		return "resume.task_remote"
	case m.tasks.connect == nil, m.served() && (m.tasks.cl == nil || s.State == projects.Offline):
		return "resume.task_offline"
	case m.served() && s.State == projects.Outdated:
		return "resume.task_old"
	case m.served() && s.Here == "":
		return "resume.task_no_node"
	case m.served() && s.Owner(s.Here) != s.Viewer.User:
		return "resume.task_not_owner"
	case m.sessionBusy(r):
		return "resume.task_busy"
	case len(m.continueAgents(r)) == 0:
		return "resume.task_no_agent"
	}
	return ""
}

// sessionBusy: r's session is open in a terminal or an app, or a run of the coordinator is on it; a background run
// would write the same transcript.
func (m *Model) sessionBusy(r *tend.Rec) bool {
	if _, ok := m.liveOf(r); ok {
		return true
	}
	if st := m.tasks.st; st != nil {
		for _, run := range st.Runs {
			if task.Open(run.State) && (run.Session == r.SessionID || run.Resume == r.SessionID) {
				return true
			}
		}
	}
	return r.Provider == tend.ProviderCodex && !m.now.IsZero() && !r.LastAt.IsZero() && m.now.Sub(r.LastAt) < agent.CodexQuiet
}

// makeMachine is this machine's name to the coordinator.
func (m *Model) makeMachine() string {
	if m.served() {
		return m.proj.snap.Here
	}
	return coord.Local
}

// continueAgents are the profiles that can go on with r's session here, the one named after its provider first; before
// the coordinator listed them, the provider's own.
func (m *Model) continueAgents(r *tend.Rec) []string {
	machine := m.makeMachine()
	var out []string
	for _, p := range m.tasks.agents {
		if agent.CanContinue(p, r.Provider, machine) {
			out = append(out, p.Name)
		}
	}
	if len(m.tasks.agents) == 0 && agent.CanContinue(tend.AgentProfile{Name: r.Provider, Provider: r.Provider}, r.Provider, machine) {
		out = []string{r.Provider}
	}
	if i := slices.Index(out, r.Provider); i > 0 {
		out = append([]string{r.Provider}, slices.Delete(out, i, i+1)...)
	}
	return out
}

// takesPart: the viewer may put a task into p, as the coordinator's roleIn decides.
func (m *Model) takesPart(p *task.Project) bool {
	v := m.proj.snap.Viewer
	return p.Role(v.User) == task.RoleParticipant || v.Admin && (!m.served() || !p.Personal())
}

// continueProjects are the form's project choices: private, the project r's directory is in, then the others the
// viewer takes part in by name.
func (m *Model) continueProjects(r *tend.Rec) (ids, labels []string) {
	ids, labels = []string{""}, []string{i18n.T("tasks.project_private")}
	s := &m.proj.snap
	if !s.Ready() {
		return ids, labels
	}
	own := s.Of(r)
	var ps []*task.Project
	for _, p := range s.Projects {
		if p != own && m.takesPart(p) {
			ps = append(ps, p)
		}
	}
	slices.SortFunc(ps, func(a, b *task.Project) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	if own != nil && m.takesPart(own) {
		ps = append([]*task.Project{own}, ps...)
	}
	for _, p := range ps {
		l := render.OneLine(p.Name)
		if projects.Personal(p) {
			l = i18n.F("tasks.project_personal", l)
		}
		if p == own {
			l = i18n.F("tasks.project_of_dir", l)
		}
		ids, labels = append(ids, p.ID), append(labels, l)
	}
	return ids, labels
}

// makeTaskBtn is the resume dialog's 「建成任务」; it says why instead, greyed, when the session cannot become one.
func (m *Model) makeTaskBtn(r *tend.Rec) btn {
	if why := m.makeTaskWhy(r); why != "" {
		return btn{label: i18n.T(why)}
	}
	return btn{i18n.T("resume.btn_make_task"), false, func(mm *Model) { mm.pending = mm.makeTask(mm.ovRec()) }}
}

// makeTask opens the form on r once the coordinator is reached, or says why it cannot.
func (m *Model) makeTask(r *tend.Rec) tea.Cmd {
	if r != nil && m.shared(r.Host) {
		m.flash(i18n.F("remote.shared_note", m.ownerName(r.Host)))
		return nil
	}
	if why := m.makeTaskWhy(r); why != "" {
		m.flash(i18n.T(why))
		return nil
	}
	m.closeOverlay()
	return m.withCoord(func(mm *Model) tea.Cmd { return mm.openMakeTask(r) })
}

func (m *Model) openMakeTask(r *tend.Rec) tea.Cmd {
	area := newArea()
	area.CharLimit = 256 << 10
	area.ShowLineNumbers = false
	area.Prompt = ""
	area.SetHeight(4)
	ids, labels := m.continueProjects(r)
	m.ov = overlay{kind: ovMakeTask, focus: -1, rec: r, area: area, opts: [][]string{m.continueAgents(r), labels}, pick: []int{0, 0}, ids: ids}
	return m.ov.area.Focus()
}

// syncMakeAgents keeps the open form's agent choices in step with the profiles the coordinator listed.
func (m *Model) syncMakeAgents() {
	if m.ov.kind != ovMakeTask {
		return
	}
	was := m.picked(0)
	m.ov.opts[0] = m.continueAgents(m.ov.rec)
	m.ov.pick[0] = choice(m.ov.opts[0], was)
}

func (m *Model) makeField(i int) tea.Cmd {
	stops := makeButtons + len(m.ov.btns)
	i = (i + stops) % stops
	m.ov.area.Blur()
	m.ov.field, m.ov.focus = min(i, makeButtons), -1
	switch {
	case i == makeText:
		return m.ov.area.Focus()
	case i >= makeButtons:
		m.ov.focus = i - makeButtons
	}
	return nil
}

func (m *Model) makeTaskKey(msg tea.KeyPressMsg) tea.Cmd {
	f, stop := m.ov.field, m.ov.field
	if f == makeButtons {
		stop += max(0, m.ov.focus)
	}
	switch a := keyAct(inTaskForm, msg.String()); a {
	case actClose:
		m.closeOverlay()
		return nil
	case actSave:
		return m.submitMakeTask()
	case actEnter:
		if f != makeText {
			if !m.pressFocused() {
				return m.submitMakeTask()
			}
			return nil
		}
	case actFocusNext:
		return m.makeField(stop + 1)
	case actFocusPrev:
		return m.makeField(stop - 1)
	case actUp, actDown:
		if f != makeText {
			return m.makeField(stop + map[act]int{actUp: -1, actDown: 1}[a])
		}
	case actLeft, actRight:
		d := map[act]int{actLeft: -1, actRight: 1}[a]
		switch f {
		case makeAgent, makeProject:
			m.pickNext(f-makeAgent, d)
			return nil
		case makeButtons:
			m.moveFocus(d)
			return nil
		}
	}
	if f != makeText {
		return nil
	}
	var cmd tea.Cmd
	m.ov.area, cmd = m.ov.area.Update(msg)
	return cmd
}

// madeTaskMsg: run.continue answered; project is what the form asked for.
type madeTaskMsg struct {
	run     task.Run
	title   string
	project string
	err     error
}

func (msg madeTaskMsg) apply(m *Model) tea.Cmd {
	switch {
	case msg.err != nil:
		m.flash(i18n.F("tasks.failed", reasonText(msg.err)))
	case msg.project != "" && msg.run.Project == "": // an older coordinator ignores project
		m.flash(i18n.F("tasks.made_private", msg.run.Task))
	default:
		m.flash(i18n.F("tasks.made_from", msg.run.Task, render.Truncate(render.OneLine(msg.title), 40), keyOf(inResume, actTask)))
	}
	return nil
}

func (m *Model) submitMakeTask() tea.Cmd {
	text := strings.TrimSpace(m.ov.area.Value())
	if text == "" {
		m.flash(i18n.T("tasks.from_empty"))
		return m.makeField(makeText)
	}
	r := m.ovRec()
	if why := m.makeTaskWhy(r); why != "" {
		m.flash(i18n.T(why))
		return nil
	}
	cl := m.tasks.cl
	if cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	p := coord.Continue{Machine: m.makeMachine(), Provider: r.Provider, Session: r.SessionID, Dir: r.Cwd, Title: r.Title, Text: text,
		Agent: m.picked(0), Project: m.ov.ids[m.ov.pick[1]]}
	m.closeOverlay()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var run task.Run
		err := cl.CallCommand(ctx, coord.MRunContinue, commandID(), p, &run)
		return madeTaskMsg{run: run, title: p.Title, project: p.Project, err: err}
	}
}

func (m *Model) renderMakeTask() string {
	w := m.ovWidth()
	inner := w - 4
	r := m.ov.rec
	sub := i18n.F("tasks.from_sub", render.OneLine(r.Title), i18n.T("remote.local"), tend.ProviderName(r.Provider))
	body := []string{boldSty.Foreground(cText).Render(i18n.T("tasks.from_title")), dimmed.Render(render.Truncate(sub, inner)),
		dimmed.Render(render.Truncate(i18n.T("tasks.from_hint"), inner)), ""}
	sty := panelSty
	if m.ov.field == makeText {
		sty = panelSty.BorderForeground(cAccent)
	}
	body = append(body, dimmed.Render(i18n.T("tasks.from_text")))
	m.ov.area.SetWidth(inner - 4)
	lines := strings.Split(sty.Width(inner).Render(m.ov.area.View()), "\n")
	for row := range lines {
		m.mark(len(body)+1+row, ovPad, inner, func(mm *Model) {
			mm.pending = mm.makeField(makeText)
			mm.placeAreaCursor(row-1, 2)
		})
	}
	body = append(append(body, lines...), "")
	m.selector(&body, i18n.T("tasks.field_agent"), makeAgent, 0, inner, m.ov.field == makeAgent)
	m.selector(&body, i18n.T("tasks.field_project"), makeProject, 1, inner, m.ov.field == makeProject)
	notes := []string{i18n.T("tasks.from_privacy")}
	if id := m.ov.ids[m.ov.pick[1]]; id != "" {
		if p := m.proj.snap.Projects[id]; p != nil && !projects.Personal(p) {
			notes = append(notes, i18n.F("tasks.from_members", render.OneLine(p.Name)))
		}
	}
	notes = append(notes, i18n.T("tasks.from_resumes"))
	body = body[:len(body)-1]
	for _, n := range notes {
		for _, l := range render.Wrap(n, inner) {
			body = append(body, dimmed.Render(l))
		}
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, []btn{
		{keyed(keyOf(inTaskForm, actSave), i18n.T("tasks.btn_make")), true, func(mm *Model) { mm.pending = mm.submitMakeTask() }},
		cancelBtn(),
	})...)
	return ovRender(body, w)
}

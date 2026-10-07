package tui

import (
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
)

// ovRec is the dialog's record, re-read from the store (ov.rec is stale after a store reload).
func (m *Model) ovRec() *tend.Rec {
	r := m.ov.rec
	if r != nil && r.ID != "" && r.Host == "" {
		if cur := m.store.Get(r.ID); cur != nil {
			return cur
		}
	}
	return r
}

func (m *Model) doFork() {
	r := m.ovRec()
	p, err := capture.PlanFork(r, false)
	if err != nil {
		m.flash(err.Error())
		return
	}
	m.startPlan(r, p, false)
}

// startPlan opens a new session (fork, handoff): a new Herdr tab, or this terminal once the TUI quits.
func (m *Model) startPlan(r *tend.Rec, p capture.Plan, _ bool) {
	if c := p.Blocking(); c != nil {
		m.flash(i18n.F("start.failed", c.Text))
		return
	}
	if p.Ws == nil && len(p.WsChoices) > 1 {
		m.pickWorkspace(r, p, (*Model).startPlan)
		return
	}
	if p.Ws == nil {
		m.finish(Result{Start: &p.Spec})
		return
	}
	m.ov = overlay{}
	m.flash(i18n.F("start.opening_tab", p.Ws.Label))
	m.pending = func() tea.Msg {
		_, warn, err := p.RunInHerdr(r)
		return herdrDoneMsg{rec: r, msg: i18n.F("start.opened_tab", p.Ws.Label, capture.TabLabel(r)), warn: warn, err: err}
	}
}

type handoffMsg struct {
	rec  *tend.Rec
	path string
	err  error
}

type handoffEditedMsg struct{ err error }

// doHandoff writes the handoff pack in the background, then shows it.
func (m *Model) doHandoff() {
	r := m.ovRec()
	m.closeOverlay()
	m.flash(i18n.T("handoff.writing"))
	m.pending = func() tea.Msg {
		path, err := capture.WriteHandoff(r)
		return handoffMsg{r, path, err}
	}
}

func (m *Model) openHandoff(msg handoffMsg) {
	if msg.err != nil {
		m.flash(i18n.F("handoff.failed", msg.err.Error()))
		return
	}
	m.notice = ""
	m.ov = overlay{kind: ovHandoff, rec: msg.rec, title: msg.path, focus: -1, providers: handoffProviders(msg.rec),
		handoff: &handoffTo{dir: msg.rec.Cwd, how: "handoff.dir.same"}}
	if len(m.ov.providers) > 0 {
		m.ov.plan, _ = capture.PlanStart(msg.rec, m.ov.providers[0], capture.HandoffPrompt(msg.path), false)
	}
	m.loadHandoff()
}

func (m *Model) loadHandoff() {
	b, err := os.ReadFile(m.ov.title)
	if err != nil {
		m.flash(i18n.F("handoff.failed", err.Error()))
		return
	}
	m.ov.lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// handoffProviders: the installed CLIs, the session's own first.
func handoffProviders(r *tend.Rec) []string {
	var out []string
	for _, p := range []string{r.Provider, tend.ProviderClaude, tend.ProviderCodex} {
		if capture.Installed(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) startHandoff(provider string) {
	m.startWith(provider, capture.HandoffPrompt(m.ov.title))
}

func (m *Model) copyHandoff() {
	b, err := os.ReadFile(m.ov.title)
	if err == nil {
		err = copyText(string(b))
	}
	if err != nil {
		m.flash(i18n.F("handoff.failed", err.Error()))
		return
	}
	m.flash(i18n.T("handoff.copied"))
}

// editHandoff opens the pack in $VISUAL / $EDITOR (the TUI waits), else in VS Code (it does not).
func (m *Model) editHandoff() tea.Cmd {
	path := m.ov.title
	if parts := shell.Editor(); len(parts) > 0 {
		c := exec.Command(parts[0], append(parts[1:], path)...)
		return tea.ExecProcess(c, func(err error) tea.Msg { return handoffEditedMsg{err} })
	}
	if exec.Command("code", path).Start() == nil {
		m.flash(i18n.T("handoff.opened_code"))
		return nil
	}
	m.flash(i18n.T("cli.edit.no_editor"))
	return nil
}

func (m *Model) handoffGroups() []btnGroup {
	ssh, _ := m.handoffSSH()
	away := m.handoffAway()
	var start []btn
	for i, p := range m.ov.providers {
		b := btn{keyed(keyOf(inHandoff, providerAct(p)), tend.ProviderLabel(p)), i == 0 && ssh, func(mm *Model) { mm.handoffTerminal(p) }}
		if !ssh {
			b.act = nil
		}
		start = append(start, b)
	}
	if away {
		task := btn{i18n.T("resume.btn_make_task"), false, func(mm *Model) { mm.pending = tea.Batch(mm.pending, mm.handoffTask()) }}
		if why := m.handoffTaskWhy(); why != "" {
			task = btn{label: i18n.T(why)}
		}
		start = append(start, task)
	}
	copyLabel := "handoff.btn_copy"
	if d := m.ov.handoff; d != nil && d.host != "" {
		copyLabel = "handoff.btn_copy_command"
	}
	gs := []btnGroup{}
	if m.hosts != nil && m.ov.handoff != nil {
		where := []btn{
			{keyed(keyOf(inHandoff, actHost), i18n.T("handoff.btn_machine")), false, (*Model).pickHandoffHost},
		}
		if away {
			where = append(where, btn{keyed(keyOf(inHandoff, actDir), i18n.T("handoff.btn_dir")), false, (*Model).pickHandoffDir})
		}
		if away && m.canRecord() {
			where = append(where, btn{keyed(keyOf(inHandoff, actTick), i18n.T("handoff.btn_record")), false, (*Model).tickHandoffRecord})
		}
		gs = append(gs, btnGroup{label: i18n.T("handoff.group.where"), bs: where})
	}
	return append(gs,
		btnGroup{label: i18n.T("handoff.group.start"), bs: start},
		btnGroup{label: i18n.T("handoff.group.pack"), bs: []btn{
			{keyed(keyOf(inHandoff, actEdit), i18n.T("handoff.btn_edit")), false, func(mm *Model) { mm.pending = mm.editHandoff() }},
			{keyed(keyOf(inHandoff, actCopy), i18n.T(copyLabel)), !ssh, (*Model).handoffCopy},
		}, end: []btn{cancelBtn()}},
	)
}

func (m *Model) tickHandoffRecord() {
	if d := m.ov.handoff; m.canRecord() && m.handoffAway() {
		d.record = !d.record
	}
}

// handoffWhere are the dialog's lines on where the new session starts: the machine, the directory and how it was
// found, whether it goes into the project, and why the new session cannot open in a terminal from here.
func (m *Model) handoffWhere(body *[]string, inner int) {
	d := m.ov.handoff
	if d == nil || m.hosts == nil {
		return
	}
	line := func(label, value string, click func(*Model)) {
		lw := 8
		m.mark(len(*body)+1, ovPad, inner, click)
		*body = append(*body, dimmed.Render(render.Pad(label, lw))+fit(value, inner-lw))
	}
	host := hostLabel(d.host)
	if d.reading {
		host = i18n.F("handoff.reading", hostLabel(d.want))
	}
	line(i18n.T("handoff.field_machine"), host, (*Model).pickHandoffHost)
	if !m.handoffAway() {
		return
	}
	dir := d.dir
	how := ""
	switch d.how {
	case "handoff.dir.project":
		name := ""
		if p := m.proj.snap.Of(m.ovRec()); p != nil {
			name = p.Name
		}
		how = i18n.F(d.how, name)
	case "handoff.dir.found_many":
		how = i18n.F(d.how, len(d.dirs), keyOf(inHandoff, actDir))
	case "handoff.dir.picked":
		how = i18n.F(d.how, len(d.dirs))
	case "handoff.dir.none":
		how = i18n.F(d.how, keyOf(inHandoff, actDir))
	case "":
	default:
		how = i18n.T(d.how)
	}
	if dir == "" {
		line(i18n.T("handoff.field_dir"), warnSty.Render(how), (*Model).pickHandoffDir)
	} else {
		line(i18n.T("handoff.field_dir"), dir+"  "+dimmed.Render(how), (*Model).pickHandoffDir)
	}
	switch {
	case d.comparing:
		line(i18n.T("handoff.field_env"), dimmed.Render(i18n.F("handoff.env_reading", hostLabel(d.host))), nil)
	case d.envWhy != "":
		line(i18n.T("handoff.field_env"), warnSty.Render(i18n.F("handoff.env.unread", d.envWhy)), nil)
	case d.env != nil && d.env.Blocked():
		line(i18n.T("handoff.field_env"), warnSty.Render(i18n.F("handoff.env_counts", d.env.Block, d.env.Unequal)), nil)
	case d.env != nil:
		line(i18n.T("handoff.field_env"), i18n.F("handoff.env_counts", d.env.Block, d.env.Unequal), nil)
	}
	if m.canRecord() {
		mark := "[ ]"
		if d.record {
			mark = "[x]"
		}
		name := i18n.T("handoff.record_new")
		if p := m.proj.snap.Of(m.ovRec()); p != nil {
			name = i18n.F("handoff.record_into", p.Name)
		}
		line(i18n.T("handoff.field_project"), mark+" "+name, (*Model).tickHandoffRecord)
	}
	if ok, why := m.handoffSSH(); !ok {
		for _, l := range render.Wrap(why, inner) {
			*body = append(*body, warnSty.Render(l))
		}
	}
}

func (m *Model) renderHandoff() string {
	w := m.ovWidth()
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(i18n.T("handoff.heading")))
	m.handoffWhere(&body, inner)
	extra := len(body) - 1
	if m.hosts != nil && m.ov.handoff != nil {
		extra += 3 // the 「去哪」 buttons
	}
	if m.ov.title != "" {
		m.mark(len(body)+1, ovPad, inner, func(mm *Model) { mm.pending = mm.editHandoff() })
		body = append(body, dimmed.Render(render.Truncate(paths.Tilde(m.ov.title), inner)))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))

	var text []string
	for _, l := range m.ov.lines {
		for _, wl := range render.Wrap(l, inner) {
			if strings.HasPrefix(l, "#") {
				wl = accent.Render(wl)
			}
			text = append(text, wl)
		}
	}
	end := m.scrollWindow(len(text), max(3, m.h-18-extra))
	body = append(body, text[m.ov.cursor:end]...)
	if rest := len(text) - end; rest > 0 {
		body = append(body, dimmed.Render(i18n.F("handoff.more", rest)))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))

	if m.ov.plan.Spec.Exec != "" && !m.handoffAway() {
		body = append(body, accent.Render(i18n.T("resume.target")))
		body = append(body, render.Wrap(m.ov.plan.Target(m.ov.rec, "  "+render.GlyphArrow+"  "), inner)...)
	}
	for _, l := range render.Wrap(i18n.T("handoff.note"), inner) {
		body = append(body, dimmed.Render(l))
	}
	body = append(body, "")
	body = append(body, m.buttonGroups(len(body)+1, m.handoffGroups())...)
	return ovRender(body, w)
}

// handoffEnter runs the primary button: the first CLI, or (no ssh to the other machine) copying the command to run there.
func (m *Model) handoffEnter() {
	if ok, _ := m.handoffSSH(); !ok {
		m.handoffCopy()
		return
	}
	if len(m.ov.providers) > 0 {
		m.handoffTerminal(m.ov.providers[0])
	}
}

// focusHandoffKey: on another machine 1 and 2 only focus their button, which a second press or Enter runs.
func (m *Model) focusHandoffKey(a act) {
	key := keyOf(inHandoff, a)
	for i, b := range m.ov.btns {
		if k, _, ok := labelKey(b.label); ok && k == key && b.act != nil {
			m.focusOrPress(i)
			return
		}
	}
	if ok, why := m.handoffSSH(); !ok {
		m.flash(why)
	}
}

func (m *Model) handoffKey(msg tea.KeyPressMsg) tea.Cmd {
	switch a := keyAct(inHandoff, msg.String()); a {
	case actClaude, actCodex:
		if m.handoffAway() {
			m.focusHandoffKey(a)
		} else {
			m.selectProvider(providerOf(a))
		}
	case actEdit:
		if m.ov.title != "" {
			return m.editHandoff()
		}
	case actCopy:
		m.handoffCopy()
	case actHost:
		m.pickHandoffHost()
	case actDir:
		m.pickHandoffDir()
	case actTick:
		m.tickHandoffRecord()
	default:
		if !m.scrollKey(a) {
			m.dialogKey(a, m.handoffEnter)
		}
	}
	return nil
}

package tui

import (
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// ovRec is the dialog's record, re-read from the store (ov.rec is stale after a store reload).
func (m *Model) ovRec() *fav.Rec {
	r := m.ov.rec
	if r != nil && r.ID != "" {
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
func (m *Model) startPlan(r *fav.Rec, p capture.Plan, _ bool) {
	if c := p.Blocking(); c != nil {
		m.flash(i18n.T("start.failed") + c.Text)
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
	rec  *fav.Rec
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
	m.ov = overlay{kind: ovHandoff, rec: msg.rec, title: msg.path, focus: -1, providers: handoffProviders(msg.rec)}
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
func handoffProviders(r *fav.Rec) []string {
	var out []string
	for _, p := range []string{r.Provider, fav.ProviderClaude, fav.ProviderCodex} {
		if capture.Installed(p) && !contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	return slices.Contains(ss, s)
}

func (m *Model) startHandoff(provider string) {
	r := m.ov.rec
	if !capture.Installed(provider) {
		m.flash(providerLabel(provider) + i18n.T("resume.check.not_installed"))
		return
	}
	p := m.ov.plan
	if len(m.ov.providers) == 0 || provider != m.ov.providers[0] {
		var err error
		if p, err = capture.PlanStart(r, provider, capture.HandoffPrompt(m.ov.title), false); err != nil {
			m.flash(err.Error())
			return
		}
	}
	m.startPlan(r, p, false)
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
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if parts := strings.Fields(ed); len(parts) > 0 {
		c := exec.Command(parts[0], append(parts[1:], path)...)
		return tea.ExecProcess(c, func(err error) tea.Msg { return handoffEditedMsg{err} })
	}
	if lookPath("code") != "" && exec.Command("code", path).Start() == nil {
		m.flash(i18n.T("handoff.opened_code"))
		return nil
	}
	m.flash(i18n.T("cli.edit.no_editor"))
	return nil
}

func (m *Model) handoffGroups() []btnGroup {
	var start []btn
	for i, p := range m.ov.providers {
		start = append(start, btn{keyed(keyOf(inHandoff, providerAct(p)), providerLabel(p)), i == 0, func(mm *Model) { mm.startHandoff(p) }})
	}
	return []btnGroup{
		{label: i18n.T("handoff.group.start"), bs: start},
		{label: i18n.T("handoff.group.pack"), bs: []btn{
			{keyed(keyOf(inHandoff, actEdit), i18n.T("handoff.btn_edit")), false, func(mm *Model) { mm.pending = mm.editHandoff() }},
			{keyed(keyOf(inHandoff, actCopy), i18n.T("handoff.btn_copy")), false, (*Model).copyHandoff},
		}, end: []btn{cancelBtn()}},
	}
}

func (m *Model) renderHandoff() string {
	w := m.ovWidth()
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(i18n.T("handoff.heading")))
	m.mark(len(body)+1, ovPad, inner, func(mm *Model) { mm.pending = mm.editHandoff() })
	body = append(body, dimmed.Render(render.Truncate(paths.Tilde(m.ov.title), inner)))
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
	room := max(3, m.h-18)
	m.ov.scrollMax = max(0, len(text)-room)
	m.ov.cursor = min(max(m.ov.cursor, 0), m.ov.scrollMax)
	end := min(len(text), m.ov.cursor+room)
	body = append(body, text[m.ov.cursor:end]...)
	if rest := len(text) - end; rest > 0 {
		body = append(body, dimmed.Render(i18n.F("handoff.more", rest)))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))

	if m.ov.plan.Spec.Exec != "" {
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

func (m *Model) handoffKey(msg tea.KeyMsg) tea.Cmd {
	room := max(3, m.h-18)
	switch a := keyAct(inHandoff, msg.String()); a {
	case actEnter:
		if m.pressFocused() {
			return nil
		}
		if len(m.ov.providers) > 0 {
			m.startHandoff(m.ov.providers[0])
		}
	case actClaude, actCodex:
		m.selectProvider(providerOf(a))
	case actEdit:
		return m.editHandoff()
	case actCopy:
		m.copyHandoff()
	case actDown:
		m.ov.cursor++
	case actUp:
		m.ov.cursor--
	case actPageDown:
		m.ov.cursor += room
	case actPageUp:
		m.ov.cursor -= room
	case actFocusPrev:
		m.moveFocus(-1)
	case actFocusNext:
		m.moveFocus(1)
	case actClose:
		m.closeOverlay()
	}
	return nil
}

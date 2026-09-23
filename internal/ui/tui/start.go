package tui

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// newSessionDir: r's directory (the main checkout for a removed worktree), or with r nil on a projects group header
// the group's most used directory that still exists.
func (m *Model) newSessionDir(r *fav.Rec) (dir, name string) {
	if r != nil {
		for _, d := range []string{r.Cwd, r.Repo} {
			if paths.IsDir(d) {
				return d, r.Project
			}
		}
		return "", r.Project
	}
	if m.view != viewProjects {
		return "", ""
	}
	g := m.groupUnderCursor()
	for _, d := range topN(m.groups[g], func(r *fav.Rec) string { return r.Cwd }, 8) {
		if paths.IsDir(d.key) {
			return d.key, g
		}
	}
	return "", g
}

// askStart: a new session in r's directory; the sessions already running there come first.
func (m *Model) askStart(r *fav.Rec) {
	if m.view == viewLive || m.inTrash() {
		return
	}
	dir, name := m.newSessionDir(r)
	if dir == "" {
		m.flash(i18n.T("start.no_dir"))
		return
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	r = &fav.Rec{Title: name, Cwd: dir}
	ov := overlay{kind: ovStart, rec: r, focus: -1, cursor: -1, providers: startProviders(m.projectProviders(dir))}
	for id, l := range m.live {
		cur := m.bySession(id)
		cwd, repo := l.Cwd, ""
		if cur != nil {
			cwd, repo = cur.Cwd, cur.Repo
		} else {
			cur = &fav.Rec{Provider: l.Agent, SessionID: id, Title: l.Title, Cwd: l.Cwd}
		}
		if paths.Nested(cwd, dir) || repo != "" && paths.Nested(repo, dir) {
			ov.running = append(ov.running, cur)
		}
	}
	sort.Slice(ov.running, func(i, j int) bool {
		return m.live[ov.running[i].SessionID].Since.After(m.live[ov.running[j].SessionID].Since)
	})
	if len(ov.providers) > 0 {
		ov.plan, _ = capture.PlanStart(r, ov.providers[0], "", false)
	}
	m.ov = ov
}

// askStartFromDialog: the resume dialog's "new session" starts in that session's directory.
func (m *Model) askStartFromDialog() {
	r := m.ovRec()
	m.closeOverlay()
	m.askStart(r)
}

// projectProviders: providers of the sessions under dir, most used first.
func (m *Model) projectProviders(dir string) []string {
	n := map[string]int{}
	for _, rs := range [][]*fav.Rec{m.store.All(), m.unfav} {
		for _, r := range rs {
			if paths.Nested(r.Cwd, dir) {
				n[r.Provider]++
			}
		}
	}
	out := []string{fav.ProviderClaude, fav.ProviderCodex}
	sort.SliceStable(out, func(i, j int) bool { return n[out[i]] > n[out[j]] })
	return out
}

func startProviders(order []string) []string {
	var out []string
	for _, p := range order {
		if capture.Installed(p) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) startWith(provider, prompt string) {
	r := m.ov.rec
	if !capture.Installed(provider) {
		m.flash(i18n.F("resume.check.not_installed", fav.ProviderLabel(provider)))
		return
	}
	p := m.ov.plan
	if len(m.ov.providers) == 0 || provider != m.ov.providers[0] {
		var err error
		if p, err = capture.PlanStart(r, provider, prompt, false); err != nil {
			m.flash(err.Error())
			return
		}
	}
	m.startPlan(r, p, false)
}

// switchRunning hands over to the resume dialog of a session already running here (Enter there switches to it).
func (m *Model) switchRunning(i int) {
	if i < 0 || i >= len(m.ov.running) {
		return
	}
	r := m.ov.running[i]
	m.closeOverlay()
	m.openResume(r)
}

func (m *Model) startGroups() []btnGroup {
	var bs []btn
	for i, p := range m.ov.providers {
		bs = append(bs, btn{keyed(keyOf(inStart, providerAct(p)), fav.ProviderLabel(p)), i == 0 && m.ov.cursor < 0, func(mm *Model) { mm.startWith(p, "") }})
	}
	return []btnGroup{{label: i18n.T("start.group"), bs: bs, end: []btn{cancelBtn()}}}
}

func (m *Model) renderStart() string {
	w := m.ovWidth()
	inner := w - 4
	r := m.ov.rec
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(i18n.F("start.title", r.Title), inner)))
	body = append(body, dimmed.Render(render.Truncate(paths.Tilde(r.Cwd), inner)))
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	if len(m.ov.running) == 0 {
		body = append(body, dimmed.Render(i18n.T("start.none_running")))
	} else {
		body = append(body, accent.Render(i18n.F("start.running", len(m.ov.running))))
		room := max(1, m.h-16)
		for i, lr := range m.ov.running {
			if i == room {
				body = append(body, dimmed.Render(i18n.F("project.more", len(m.ov.running)-i)))
				break
			}
			label, _ := m.needLabel(lr.SessionID, m.live[lr.SessionID])
			where := paths.Tilde(lr.Cwd)
			if lr.Cwd == r.Cwd {
				where = ""
			}
			tail := strings.TrimSpace(label + "  " + where)
			title := render.Truncate(lr.Title, inner-render.Width(tail)-6)
			line := "  " + render.GlyphLive + " " + title + strings.Repeat(" ", max(1, inner-render.Width(title)-render.Width(tail)-4)) + tail
			m.mark(len(body)+1, ovPad, inner, func(mm *Model) { mm.switchRunning(i) })
			if i == m.ov.cursor {
				line = selTitle.Render(render.Pad(line, inner))
			}
			body = append(body, line)
		}
		body = append(body, dimmed.Render(i18n.T("start.running_hint")))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	if m.ov.plan.Spec.Exec != "" {
		body = append(body, accent.Render(i18n.T("resume.target")))
		body = append(body, render.Wrap(m.ov.plan.Target(r, "  "+render.GlyphArrow+"  "), inner)...)
	} else if len(m.ov.providers) == 0 {
		body = append(body, errSty.Render(i18n.T("start.no_cli")))
	}
	body = append(body, "")
	body = append(body, m.buttonGroups(len(body)+1, m.startGroups())...)
	return ovRender(body, w)
}

func (m *Model) startKey(msg tea.KeyPressMsg) tea.Cmd {
	switch a := keyAct(inStart, msg.String()); a {
	case actClaude, actCodex:
		m.ov.cursor = -1
		m.selectProvider(providerOf(a))
	case actDown:
		m.ov.cursor = min(m.ov.cursor+1, len(m.ov.running)-1)
		m.ov.focus = -1
	case actUp:
		m.ov.cursor = max(m.ov.cursor-1, -1)
		m.ov.focus = -1
	default:
		if a == actFocusPrev || a == actFocusNext {
			m.ov.cursor = -1
		}
		m.dialogKey(a, func() {
			if m.ov.cursor >= 0 {
				m.switchRunning(m.ov.cursor)
			} else if len(m.ov.providers) > 0 {
				m.startWith(m.ov.providers[0], "")
			}
		})
	}
	return nil
}

func providerOf(a act) string {
	if a == actCodex {
		return fav.ProviderCodex
	}
	return fav.ProviderClaude
}

func providerAct(p string) act {
	if p == fav.ProviderCodex {
		return actCodex
	}
	return actClaude
}

// selectProvider (1 / 2 in the new-session and handoff dialogs) focuses that provider's button; the same digit again or
// Enter starts it.
func (m *Model) selectProvider(p string) {
	if i := slices.Index(m.ov.providers, p); i >= 0 {
		m.focusOrPress(i)
		return
	}
	m.flash(i18n.F("resume.check.not_installed", fav.ProviderLabel(p)))
}

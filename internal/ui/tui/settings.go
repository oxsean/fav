package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// Settings panel: , opens; ↑↓ picks, ←→/Enter/Space changes a value; applies immediately and writes config.json.

type setting struct {
	label string
	opts  []string
	get   func(m *Model) int
	set   func(m *Model, i int) tea.Cmd
	text  func(m *Model) *string // non-nil = free-text item: Enter edits, Enter saves, Esc discards
	hint  string
}

var views = []string{"favorites", "sessions", "projects", "live"}
var sorts = []string{"active", "started", "favorited", "turns"}
var langs = []string{"", i18n.ZH, i18n.EN}
var turnsOpts = []int{1, 2, 3, 5, 8}
var wheelOpts = []int{1, 2, 3, 5}
var trashOpts = []int{7, 30, 90, 0}
var outputOpts = []int{0, 3, 10, 30}
var resumeIns = []string{fav.ResumeTerminal, fav.ResumeApp, fav.ResumeOrigin}

var notifies = []string{fav.NotifyOff, fav.NotifyBell}

func indexOf[T comparable](xs []T, x T) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return 0
}

func intLabels(xs []int, unit string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strconv.Itoa(x) + unit
	}
	return out
}

// settingsTable is rebuilt each time so labels follow the current language.
func settingsTable() []setting {
	return []setting{
		{i18n.T("settings.time_format"), []string{i18n.T("settings.time_relative"), i18n.T("settings.time_absolute")},
			func(m *Model) int { return map[bool]int{true: 0, false: 1}[m.cfg.RelativeTime] },
			func(m *Model, i int) tea.Cmd {
				m.cfg.RelativeTime = i == 0
				render.RelativeTime = m.cfg.RelativeTime
				return nil
			}, nil, ""},
		{i18n.T("settings.default_tab"), []string{i18n.T("view.favorites"), i18n.T("view.sessions"), i18n.T("label.projects"), "Agents"},
			func(m *Model) int { return indexOf(views, m.cfg.DefaultView) },
			func(m *Model, i int) tea.Cmd { m.cfg.DefaultView = views[i]; return nil }, nil, ""},
		{i18n.T("settings.default_sort"), []string{i18n.T("sort.active"), i18n.T("sort.started"), i18n.T("sort.favorited"), i18n.T("sort.turns")},
			func(m *Model) int { return indexOf(sorts, m.cfg.Sort) },
			func(m *Model, i int) tea.Cmd { m.cfg.Sort = sorts[i]; m.sortBy = sortBy(i); m.refresh(); return nil }, nil, ""},
		{i18n.T("settings.min_turns"), intLabels(turnsOpts, i18n.T("settings.min_turns_suffix")),
			func(m *Model) int { return indexOf(turnsOpts, m.cfg.MinTurns) },
			func(m *Model, i int) tea.Cmd {
				m.cfg.MinTurns = turnsOpts[i]
				fav.DefaultTurns = m.cfg.MinTurns
				m.recount()
				m.refresh()
				return nil
			}, nil, ""},
		{i18n.T("settings.wheel"), intLabels(wheelOpts, i18n.T("settings.wheel_suffix")),
			func(m *Model) int { return indexOf(wheelOpts, m.cfg.WheelStep) },
			func(m *Model, i int) tea.Cmd {
				m.cfg.WheelStep = wheelOpts[i]
				m.wheelStep = m.cfg.WheelStep
				setWheelTuning(m.wheelStep, m.cfg.WheelSpeed)
				return nil
			}, nil, ""},
		{i18n.T("settings.wheel_speed"), []string{i18n.T("settings.speed_off"), i18n.T("settings.speed_normal"), i18n.T("settings.speed_fast")},
			func(m *Model) int { return indexOf(wheelSpeeds, m.cfg.WheelSpeed) },
			func(m *Model, i int) tea.Cmd {
				m.cfg.WheelSpeed = wheelSpeeds[i]
				setWheelTuning(m.wheelStep, m.cfg.WheelSpeed)
				return nil
			}, nil, ""},
		{i18n.T("settings.trash_days"), []string{i18n.T("settings.trash_7"), i18n.T("settings.trash_30"), i18n.T("settings.trash_90"), i18n.T("settings.trash_forever")},
			func(m *Model) int { return indexOf(trashOpts, m.cfg.TrashDays) },
			func(m *Model, i int) tea.Cmd { m.cfg.TrashDays = trashOpts[i]; return nil }, nil, ""},
		{i18n.T("settings.notify"), []string{i18n.T("settings.notify_off"), i18n.T("settings.notify_bell")},
			func(m *Model) int { return indexOf(notifies, m.cfg.Notify) },
			func(m *Model, i int) tea.Cmd { m.cfg.Notify = notifies[i]; return nil }, nil, ""},
		{i18n.T("settings.resume_in"), []string{i18n.T("settings.resume_terminal"), i18n.T("settings.resume_app"), i18n.T("settings.resume_origin")},
			func(m *Model) int { return indexOf(resumeIns, m.cfg.ResumeIn) },
			func(m *Model, i int) tea.Cmd { m.cfg.ResumeIn = resumeIns[i]; return nil }, nil, ""},
		{i18n.T("settings.tool_output"), append([]string{i18n.T("settings.tool_output_off")}, intLabels(outputOpts[1:], i18n.T("settings.tool_output_suffix"))...),
			func(m *Model) int { return indexOf(outputOpts, m.cfg.ToolOutput) },
			func(m *Model, i int) tea.Cmd { m.cfg.ToolOutput = outputOpts[i]; return m.syncText(m.idx) }, nil, ""},
		{i18n.T("settings.icons"), []string{"ASCII", "Nerd Font"},
			func(m *Model) int { return map[bool]int{false: 0, true: 1}[m.cfg.Icons == "nerd"] },
			func(m *Model, i int) tea.Cmd {
				m.cfg.Icons = []string{"ascii", "nerd"}[i]
				if !render.IconsFromEnv() {
					render.SetIcons(m.cfg.Icons)
				}
				return nil
			}, nil, ""},
		{label: "IDE", text: func(m *Model) *string { return &m.cfg.IDE }, hint: i18n.F("settings.ide_hint", capture.DefaultIDE())},
		{i18n.T("settings.language"), []string{i18n.T("settings.lang_system"), "中文", "English"},
			func(m *Model) int { return indexOf(langs, m.cfg.Lang) },
			func(m *Model, i int) tea.Cmd { m.cfg.Lang = langs[i]; i18n.Set(i18n.Resolve(m.cfg.Lang)); return nil }, nil, ""},
		{i18n.T("settings.mouse"), []string{i18n.T("settings.mouse_on"), i18n.T("settings.mouse_off")},
			func(m *Model) int { return map[bool]int{true: 0, false: 1}[m.cfg.Mouse] },
			func(m *Model, i int) tea.Cmd {
				m.cfg.Mouse = i == 0
				m.mouse = m.cfg.Mouse
				return nil
			}, nil, ""},
	}
}

func (m *Model) openSettings() { m.ov = overlay{kind: ovSettings} }

func (m *Model) cycleSetting(i, delta int) tea.Cmd {
	s := settingsTable()[i]
	if s.text != nil {
		ti := newInput()
		ti.SetValue(*s.text(m))
		ti.Placeholder = capture.DefaultIDE()
		ti.CharLimit = 200
		ti.Focus()
		ti.CursorEnd()
		m.ov.edit, m.ov.editing = ti, true
		return textinput.Blink
	}
	n := len(s.opts)
	cmd := s.set(m, ((s.get(m)+delta)%n+n)%n)
	m.saveConfig()
	return cmd
}

func (m *Model) settingsKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.ov.editing {
		switch msg.String() {
		case "esc":
			m.ov.editing = false
			return nil
		case "enter":
			*settingsTable()[m.ov.cursor].text(m) = strings.TrimSpace(m.ov.edit.Value())
			m.ov.editing = false
			m.saveConfig()
			return nil
		}
		var cmd tea.Cmd
		m.ov.edit, cmd = m.ov.edit.Update(msg)
		return cmd
	}
	switch msg.String() {
	case "esc", "q", ",", "，":
		m.closeOverlay()
	case "up", "k", "ctrl+p", "shift+tab":
		if m.ov.cursor > 0 {
			m.ov.cursor--
		}
	case "down", "j", "ctrl+n", "tab":
		if m.ov.cursor < len(settingsTable())-1 {
			m.ov.cursor++
		}
	case "left", "h":
		return m.cycleSetting(m.ov.cursor, -1)
	case "right", "l", "enter", "space":
		return m.cycleSetting(m.ov.cursor, 1)
	}
	return nil
}

func (m *Model) renderSettings() string {
	w := m.ovWidth()
	inner := w - 4
	table := settingsTable()
	labelW := 0 // the longest label, so no row wraps or cuts its name
	for _, s := range table {
		labelW = max(labelW, render.Width(s.label)+1)
	}
	labelW = min(labelW, max(8, inner/2))
	body := []string{boldSty.Foreground(cText).Render(i18n.T("settings.title")), dimmed.Render(i18n.T("settings.hint")), ""}
	for i, s := range table {
		var val string
		switch {
		case s.text != nil && i == m.ov.cursor && m.ov.editing:
			val = inputView(m.ov.edit)
		case s.text != nil && *s.text(m) == "":
			val = s.hint
		case s.text != nil:
			val = *s.text(m)
		default:
			val = s.opts[s.get(m)]
		}
		line := render.Pad(s.label, labelW) + render.GlyphClosed + " " + val
		sty := dimmed
		if i == m.ov.cursor {
			sty = chipFoc.UnsetBorderStyle().UnsetPadding()
			line = render.Pad(s.label, labelW) + render.GlyphOpen + " " + val
		}
		idx := i
		m.mark(len(body), ovPad, inner, func(mm *Model) {
			if mm.ov.editing && mm.ov.cursor == idx {
				mm.placeCursor(&mm.ov.edit, labelW+2)
				return
			}
			mm.ov.editing = false
			mm.ov.cursor = idx
			mm.pending = mm.cycleSetting(idx, 1)
		})
		body = append(body, sty.Render(fit(line, inner)))
	}
	body = append(body, "", dimmed.Render(render.Truncate(i18n.F("settings.file_hint", paths.Tilde(fav.ConfigPath())), inner)))
	return ovRender(body, w)
}

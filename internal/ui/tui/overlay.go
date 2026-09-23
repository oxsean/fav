package tui

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

type ovKind int

const (
	ovNone ovKind = iota
	ovPicker
	ovHelp
	ovResume
	ovSettings
	ovMessage
	ovEdit
	ovConfirm
)

// ovPad: border + padding columns left of the overlay box; box-local zones add it.
const ovPad = 3

type overlay struct {
	kind      ovKind
	scrollMax int // last line offset the message / help text can scroll to, set while rendering
	title     string
	hint      string
	items     []item
	filter    textinput.Model
	checked   map[string]bool
	cursor    int
	multi     bool
	apply     func(m *Model, chosen []string)
	parse     func(string) (item, bool) // the search text itself is selectable (custom date input)
	browse    func(string) []item       // dir picker: the list follows the typed path, items unused
	btns      []btn                     // buttons drawn this frame; arrow keys move focus among them
	focus     int                       // focused button; -1 = none, Enter runs the default
	rec       *fav.Rec
	plan      capture.Plan
	edit      textinput.Model
	edit2     textinput.Model
	area      textarea.Model
	field     int
	editing   bool
	confirm   func(*Model)
	back      func(*Model) // ovConfirm cancel goes back here (nil = close)
	okLabel   string
	app       bool // resume dialog: opening in the desktop app is the primary action

	msg    capture.Message
	lines  []string
	kinds  []byte // per line: 'T' body, 'H' tool header, 'B' argument continuation, 'R' result
	stepOf []int  // step index per line, -1 = body
	boxW   int
	// cursor doubles as the scroll offset
}

func (o overlay) active() bool { return o.kind != ovNone }

// pickerFill: minimum rows the picker shows by default, padded with single-use items.
const pickerFill = 10

// visible: with no input, items used twice or more (checked and uncounted ones always), padded to pickerFill with single-use ones; with input, a full match.
func (o overlay) visible() []item {
	if o.browse != nil {
		return o.browse(o.filter.Value())
	}
	q := strings.ToLower(strings.TrimSpace(o.filter.Value()))
	if q == "" {
		var out []item
		for _, it := range o.items {
			if it.count != 1 || o.checked[it.name] {
				out = append(out, it)
			}
		}
		for _, it := range o.items {
			if len(out) >= pickerFill {
				break
			}
			if it.count == 1 && !o.checked[it.name] {
				out = append(out, it)
			}
		}
		return out
	}
	var out []item
	if o.parse != nil {
		if it, ok := o.parse(q); ok {
			out = append(out, it)
		}
	}
	for _, it := range o.items {
		if strings.Contains(strings.ToLower(it.name), q) || strings.Contains(strings.ToLower(it.label), q) {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) openPicker(title, hint string, items []item, multi bool, preset []string, apply func(*Model, []string)) {
	checked := map[string]bool{}
	for _, p := range preset {
		if p != "" {
			checked[p] = true
		}
	}
	ti := textinput.New()
	ti.Placeholder = i18n.T("picker.filter_placeholder")
	ti.Prompt = render.GlyphSearch + " "
	ti.CharLimit = 60
	ti.Focus()

	m.ov = overlay{
		kind: ovPicker, title: title, hint: hint, items: items,
		multi: multi, checked: checked, apply: apply, filter: ti, focus: -1,
	}
	if !multi {
		for i, it := range m.ov.visible() {
			if checked[it.name] {
				m.ov.cursor = i
			}
		}
	}
}

func (m *Model) closeOverlay() { m.ov = overlay{} }

// moveFocus moves between buttons without wrap-around; the first move starts from the primary button.
func (m *Model) moveFocus(delta int) {
	n := len(m.ov.btns)
	if n == 0 {
		return
	}
	from := m.ov.focus
	if from < 0 {
		for i, b := range m.ov.btns {
			if b.primary {
				from = i
			}
		}
	}
	f := from + delta
	if f < 0 {
		f = 0
	}
	if f >= n {
		f = n - 1
	}
	m.ov.focus = f
}

// placeCursor puts the cursor at the clicked column; lead = columns between the zone's left edge and the text (border, padding, prompt).
// ponytail: off by the scrolled amount once textinput scrolls horizontally; titles / paths / tags usually fit
func (m *Model) placeCursor(ti *textinput.Model, lead int) {
	col := m.clickX - lead - ansi.StringWidth(ti.Prompt)
	pos, w := 0, 0
	for _, r := range ti.Value() {
		if w >= col {
			break
		}
		w += render.Width(string(r))
		pos++
	}
	ti.Focus()
	ti.SetCursor(pos)
}

func (m *Model) clickInput() {
	m.placeCursor(&m.ov.filter, 2)
	m.ov.focus = -1
}

func (o *overlay) cancelLabel() string {
	if o.back != nil {
		return i18n.T("btn.back")
	}
	return i18n.T("btn.cancel")
}

func (m *Model) pressFocused() bool {
	if m.ov.focus < 0 || m.ov.focus >= len(m.ov.btns) {
		return false
	}
	m.ov.btns[m.ov.focus].act(m)
	return true
}

func (m *Model) clearPicker() {
	apply := m.ov.apply
	m.closeOverlay()
	if apply != nil {
		apply(m, nil)
	}
	m.refresh()
}

func (m *Model) applyPicker() {
	var chosen []string
	vis := m.ov.visible()
	if m.ov.multi {
		for _, it := range m.ov.items {
			if m.ov.checked[it.name] {
				chosen = append(chosen, it.name)
			}
		}
	} else if m.ov.cursor < len(vis) {
		chosen = []string{vis[m.ov.cursor].name}
	}
	apply := m.ov.apply
	m.closeOverlay()
	if apply != nil {
		apply(m, chosen)
	}
	m.refresh()
}

func (m *Model) renderOverlay() string {
	switch m.ov.kind {
	case ovPicker:
		return m.renderPicker()
	case ovHelp:
		return m.renderHelp()
	case ovResume:
		return m.renderResume()
	case ovSettings:
		return m.renderSettings()
	case ovMessage:
		return m.renderMessage()
	case ovEdit:
		return m.renderEdit()
	case ovConfirm:
		return m.renderConfirm()
	}
	return ""
}

// keyRe matches JSON / YAML keys: "key": or key: at line start (indent and - allowed).
var keyRe = regexp.MustCompile(`^(\s*-?\s*)("[^"]+"|[A-Za-z_][\w.-]*)(\s*:)(\s|$)`)

func colorKeys(l string, sty lipgloss.Style) string {
	loc := keyRe.FindStringSubmatchIndex(l)
	if loc == nil {
		return sty.Render(l)
	}
	return sty.Render(l[:loc[4]]) + accent.Render(l[loc[4]:loc[5]]) + sty.Render(l[loc[5]:])
}

func scrollbar(total, top, room int) []string {
	out := make([]string, room)
	for i := range out {
		out[i] = " "
	}
	if total <= room || room <= 0 {
		return out
	}
	thumb := max(1, room*room/total)
	start := (room - thumb) * top / max(1, total-room)
	for i := range out {
		if i >= start && i < start+thumb {
			out[i] = accent.Render("┃")
		} else {
			out[i] = frame.Render("│")
		}
	}
	return out
}

func (m *Model) renderMessage() string {
	w := m.ov.boxW
	room := max(1, m.h-4-6) // 2 rows top and bottom, minus border 2, title 1, blank 2, hint 1
	lines := m.ov.lines
	m.ov.scrollMax = max(0, len(lines)-room)
	m.ov.cursor = min(max(m.ov.cursor, 0), m.ov.scrollMax)
	end := min(len(lines), m.ov.cursor+room)
	who := i18n.T("chat.you")
	if m.ov.msg.Role != "user" {
		who = "AI"
	}
	title := who + " · " + render.WhenFull(m.ov.msg.At) + " · " + render.GlyphChars + " " + render.Chars(m.ov.msg.Chars)
	if p := m.probes[m.current()]; p != nil {
		title += i18n.F("msg.title_index", m.chatCur+1, len(p.msgs))
	}
	if len(lines) > room {
		title += i18n.F("overlay.line_range", m.ov.cursor+1, end, len(lines))
	}
	body := []string{boldSty.Foreground(cText).Render(title), ""}
	q := m.findQuery()
	inner := w - 4
	bar := scrollbar(len(lines), m.ov.cursor, room)
	for i := m.ov.cursor; i < end; i++ {
		l := lines[i]
		var line string
		switch m.ov.kinds[i] {
		case 'T':
			line = highlight(l, q)
		case 'H':
			tool, rest, _ := strings.Cut(l, "\x00")
			line = accent.Render(render.GlyphTerm+" "+tool) + dimmed.Render("  "+rest)
		case 'S':
			line = frame.Render(strings.Repeat(hRule, inner-2))
		default:
			line = colorKeys(l, dimmed)
		}
		body = append(body, fit(line, inner-2)+" "+bar[i-m.ov.cursor])
	}
	for len(body) < room+2 {
		body = append(body, "")
	}
	hint := i18n.T("msg.hint")
	if len(lines) > room {
		hint = i18n.T("msg.hint_scroll") + hint
	}
	body = append(body, "", dimmed.Render(hint))
	return ovRender(body, w)
}

func (m *Model) ovWidth() int {
	w := m.w * 55 / 100
	if w < 40 {
		w = m.w - 6
	}
	w = min(max(w, 20), 72)
	switch m.ov.kind {
	case ovResume: // wide enough for each button group to fit on one row
		w = min(max(w, 72, groupsWidth(m.resumeGroups())), m.w-4)
	case ovHelp:
		w = min(m.w-8, 120)
	case ovEdit:
		w = min(m.w-8, 100)
	case ovSettings:
		w = min(m.w-8, 96)
	}
	return w
}

type btn struct {
	label   string
	primary bool
	act     func(*Model)
}

// buttons draws a row of buttons as 3 lines and registers click zones, wrapping when a row does not fit.
func (m *Model) buttons(y0 int, bs []btn) []string {
	m.ov.btns = bs
	return m.btnRows(y0, ovPad, m.ovWidth()-4, bs, 0, len(bs))
}

// btnRows draws bs from column x0 within width inner; base is the focus index of bs[0]; bs[right:] are pushed to the
// right edge when they fit on the same row.
func (m *Model) btnRows(y0, x0, inner int, bs []btn, base, right int) []string {
	var out []string
	var cols [][]string
	x := x0
	flush := func() {
		for i := 0; i < 3; i++ {
			parts := make([]string, len(cols))
			for j := range cols {
				parts[j] = at(cols[j], i)
			}
			out = append(out, strings.Join(parts, " "))
		}
		cols, x = nil, x0
	}
	for i, b := range bs {
		w := ansi.StringWidth(b.label) + 4
		if len(cols) > 0 && x-x0+w > inner {
			flush()
		}
		if i == right && len(cols) > 0 {
			if sp := x0 + inner - rowWidth(bs[right:]) - x - 1; sp > 0 {
				pad := strings.Repeat(" ", sp)
				cols = append(cols, []string{pad, pad, pad})
				x += sp + 1
			}
		}
		sty, label := btnSty, keyedLabel(b.label)
		if b.primary {
			sty, label = btnPri, b.label
		}
		if base+i == m.ov.focus {
			sty, label = btnFocus, b.label
		}
		cols = append(cols, strings.Split(sty.Render(label), "\n"))
		m.markRows(y0+len(out), x, w, 3, b.act)
		x += w + 1
	}
	flush()
	return out
}

// keyedLabel puts a button's leading key in the accent colour, like the footer; a label without one stays as is.
func keyedLabel(label string) string {
	key, desc, ok := labelKey(label)
	if !ok {
		return label
	}
	return btnKey.Render(key) + btnText.Render(" "+desc)
}

// labelKey splits "f 收藏" into its key and text.
func labelKey(label string) (key, desc string, ok bool) {
	key, desc, ok = strings.Cut(label, " ")
	return key, desc, ok && isKeyName(key)
}

var keyNames = map[string]bool{"Enter": true, "Esc": true, "Tab": true, "Space": true, "Backspace": true, "PgUp": true, "PgDn": true, "Home": true, "End": true}

func isKeyName(s string) bool {
	return utf8.RuneCountInString(s) == 1 || keyNames[s] || strings.ContainsAny(s, "+/") && isASCII(s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// btnGroup is one labelled row of a dialog's buttons; end sits at the row's right edge.
type btnGroup struct {
	label string
	bs    []btn
	end   []btn
}

// buttonGroups draws each group on its own row after a dim label column; focus runs through the groups in order.
func (m *Model) buttonGroups(y0 int, gs []btnGroup) []string {
	lw := groupLabelWidth(gs)
	inner := m.ovWidth() - 4
	var all []btn
	var out []string
	for _, g := range gs {
		bs := append(g.bs[:len(g.bs):len(g.bs)], g.end...)
		rows := m.btnRows(y0+len(out), ovPad+lw, inner-lw, bs, len(all), len(g.bs))
		all = append(all, bs...)
		for k, l := range rows {
			lab := strings.Repeat(" ", lw)
			if k == 1 {
				lab = dimmed.Render(render.Pad(g.label, lw))
			}
			out = append(out, lab+l)
		}
	}
	m.ov.btns = all
	return out
}

func groupLabelWidth(gs []btnGroup) int {
	w := 0
	for _, g := range gs {
		w = max(w, ansi.StringWidth(g.label))
	}
	return w + 2
}

// groupsWidth: the overlay width that puts every group on one row.
func groupsWidth(gs []btnGroup) int {
	w := 0
	for _, g := range gs {
		row := rowWidth(g.bs)
		if len(g.end) > 0 {
			row += 3 + rowWidth(g.end)
		}
		w = max(w, row)
	}
	return groupLabelWidth(gs) + w + 4
}

// rowWidth: the columns bs take on one row (button = text + 4, gap 1).
func rowWidth(bs []btn) int {
	w := 0
	for i, b := range bs {
		if i > 0 {
			w++
		}
		w += ansi.StringWidth(b.label) + 4
	}
	return w
}

func (m *Model) renderPicker() string {
	w := m.ovWidth()
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(m.ov.title))
	if m.ov.hint != "" {
		body = append(body, dimmed.Render(m.ov.hint))
	}
	m.ov.filter.Width = inner - 6
	m.mark(len(body)+2, ovPad, inner, (*Model).clickInput)
	body = append(body, strings.Split(panelSty.Width(inner-2).Render(fit(m.ov.filter.View(), inner-4)), "\n")...)
	body = append(body, "")

	vis := m.ov.visible()
	maxRows := max(3, m.h-16)
	start := 0
	if m.ov.cursor >= maxRows {
		start = m.ov.cursor - maxRows + 1
	}
	if len(vis) == 0 {
		body = append(body, dimmed.Render(i18n.T("picker.no_match")))
	}
	for i := start; i < len(vis) && i < start+maxRows; i++ {
		it := vis[i]
		mark := "  "
		if m.ov.multi {
			mark = "[ ] "
			if m.ov.checked[it.name] {
				mark = "[" + render.GlyphOK + "] "
			}
		} else if m.ov.checked[it.name] {
			mark = render.GlyphOK + " "
		}
		count := ""
		if it.count > 0 {
			count = strconv.Itoa(it.count)
		}
		label := it.label
		if label == "" {
			label = it.name
		}
		name := render.Truncate(label, inner-len(mark)-len(count)-2)
		pad := max(0, inner-len(mark)-render.Width(name)-len(count))
		text := mark + name + strings.Repeat(" ", pad)
		line := text + dimmed.Render(count)
		if i == m.ov.cursor {
			line = accent.Bold(true).Render(text) + accent.Render(count)
		}
		idx := i
		m.mark(len(body)+1, ovPad, inner, func(mm *Model) { mm.ov.cursor = idx; mm.toggleOrApply() })
		body = append(body, line)
	}
	switch hidden := len(m.ov.items) - len(vis); {
	case len(vis) > maxRows:
		body = append(body, dimmed.Render(i18n.F("picker.more", len(vis))))
	case hidden > 0 && strings.TrimSpace(m.ov.filter.Value()) == "":
		body = append(body, dimmed.Render(i18n.F("picker.more_rare", hidden)))
	}
	hint := pickerHint(m.ov.multi)
	if m.ov.browse != nil {
		hint = i18n.T("dir.hint")
	}
	body = append(body, "", faint.Render(hint), "")

	bs := []btn{{i18n.T("btn.apply"), true, (*Model).applyPicker}, {i18n.T("btn.clear"), false, (*Model).clearPicker}, {i18n.T("btn.cancel"), false, (*Model).closeOverlay}}
	if m.ov.browse != nil {
		// no primary button: Enter in the list descends, buttons need ← (or a click)
		bs = []btn{{i18n.T("dir.btn_pick"), false, (*Model).pickDir}, {i18n.T("dir.btn_enter"), false, (*Model).descendDir}, {i18n.T("btn.cancel"), false, (*Model).closeOverlay}}
	}
	body = append(body, m.buttons(len(body)+1, bs)...)
	return ovRender(body, w)
}

func pickerHint(multi bool) string {
	if multi {
		return i18n.T("picker.hint_multi")
	}
	return i18n.T("picker.hint_single")
}

func (m *Model) toggleOrApply() {
	vis := m.ov.visible()
	if m.ov.browse != nil { // dir picker: children descend, only the "this directory" row selects
		if m.ov.cursor < len(vis) && vis[m.ov.cursor].count != -1 {
			m.descendDir()
			return
		}
		m.pickDir()
		return
	}
	if !m.ov.multi {
		m.applyPicker()
		return
	}
	if m.ov.cursor < len(vis) {
		k := vis[m.ov.cursor].name
		m.ov.checked[k] = !m.ov.checked[k]
	}
}

// helpRow: one or more key lines (alternatives on their own lines) and a one-line description.
type helpRow struct {
	keys []string
	desc string
}

type helpGroup struct {
	title string
	rows  []helpRow
}

func helpGroups() []helpGroup {
	t := i18n.T
	return []helpGroup{
		{t("help.group.search"), []helpRow{
			{[]string{"/  、"}, t("help.search")},
			{[]string{"> 》"}, t("help.msg_search")},
			{[]string{"\\  Ctrl+S"}, t("help.find")},
			{[]string{"n / N"}, t("help.next_hit")},
			{[]string{"→"}, t("help.all_hits")},
			{[]string{"o  Ctrl+O"}, t("help.sort")},
		}},
		{t("help.group.read"), []helpRow{
			{[]string{"j / k  ↑ ↓", "Ctrl+N / Ctrl+P"}, t("help.move")},
			{[]string{"PgUp / PgDn", "Ctrl+B / Ctrl+F", "Ctrl+U / Ctrl+D", "g / G  Home / End"}, t("help.page")},
			{[]string{"h / l  ← →"}, t("help.pane")},
			{[]string{"J / K  Ctrl+J/K"}, t("help.chat_move")},
			{[]string{t("help.key_enter_chat")}, t("help.enter_chat")},
		}},
		{t("help.group.resume"), []helpRow{
			{[]string{"Enter / r"}, t("help.enter")},
			{[]string{"Space  Ctrl+G"}, t("help.space")},
			{[]string{t("help.key_t_resume")}, t("help.t_resume")},
			{[]string{t("help.key_p_app")}, t("help.app")},
			{[]string{t("help.key_y_resume")}, t("help.y_resume")},
			{[]string{t("help.key_ide")}, t("help.ide")},
			{[]string{"X"}, t("help.close_tab")},
		}},
		{t("help.group.record"), []helpRow{
			{[]string{"f / *"}, t("help.favorite")},
			{[]string{"x / a", "Ctrl+X / Ctrl+A"}, t("help.done_archive")},
			{[]string{"e  Ctrl+E"}, t("help.edit")},
			{[]string{"M"}, t("help.move_project")},
			{[]string{"D"}, t("help.delete")},
		}},
		{t("help.group.view"), []helpRow{
			{[]string{"Tab / Shift+Tab", "1 2 3 4"}, t("help.tabs")},
			{[]string{t("help.key_chip_row")}, t("help.chip_row")},
			{[]string{"t / p / v / d"}, t("help.filters")},
			{[]string{"s"}, t("help.status")},
			{[]string{t("help.key_enter_group")}, t("help.enter_group")},
			{[]string{"z  - / +"}, t("help.fold_all")},
		}},
		{t("help.group.other"), []helpRow{
			{[]string{t("help.key_settings")}, t("help.settings")},
			{[]string{"Esc"}, t("help.esc")},
			{[]string{"q  Ctrl+C"}, t("help.quit")},
		}},
	}
}

// helpKeyCap: the key column never grows past this, so one long key cannot push every description to the right.
const helpKeyCap = 22

func (m *Model) renderHelp() string {
	w := m.ovWidth()
	inner := w - 4
	textW := inner - 2 // one column for the scrollbar
	groups := helpGroups()
	keyW := 0
	for _, g := range groups {
		for _, r := range g.rows {
			for _, k := range r.keys {
				keyW = max(keyW, ansi.StringWidth(k))
			}
		}
	}
	keyW = min(keyW+2, helpKeyCap, textW/2)
	var lines []string
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, boldSty.Foreground(cText).Render(g.title))
		for _, r := range g.rows {
			desc := render.Wrap(r.desc, textW-keyW-2)
			for i := 0; i < max(len(r.keys), len(desc)); i++ {
				key, seg := "", ""
				if i < len(r.keys) {
					key = render.Truncate(r.keys[i], keyW-1)
				}
				if i < len(desc) {
					seg = desc[i]
				}
				lines = append(lines, "  "+accent.Render(render.Pad(key, keyW))+seg)
			}
		}
	}
	lines = append(lines, "")
	notes := []string{
		i18n.T("help.note_msg_syntax"),
		i18n.T("help.note_query"),
		i18n.T("help.note_move"),
		i18n.T("help.note_delete"),
		i18n.T("help.note_agents"),
		i18n.T("help.note_ime"),
		i18n.T("help.note_mouse"),
		i18n.T("help.note_drag"),
	}
	for i, l := range notes {
		if i > 0 {
			lines = append(lines, "")
		}
		for _, seg := range render.Wrap(l, textW) {
			lines = append(lines, dimmed.Render(seg))
		}
	}

	// scrolls when it does not fit: cursor is the first visible line; j/k, paging and the wheel move it
	room := max(1, m.h-4-7)
	m.ov.scrollMax = max(0, len(lines)-room)
	m.ov.cursor = min(max(m.ov.cursor, 0), m.ov.scrollMax)
	end := min(len(lines), m.ov.cursor+room)
	title := i18n.T("help.title")
	if len(lines) > room {
		title += i18n.F("overlay.line_range", m.ov.cursor+1, end, len(lines))
	}
	body := []string{boldSty.Foreground(cText).Render(title), ""}
	bar := scrollbar(len(lines), m.ov.cursor, room)
	for i := m.ov.cursor; i < end; i++ {
		body = append(body, fit(lines[i], textW)+" "+bar[i-m.ov.cursor])
	}
	body = append(body, "")
	label := i18n.T("btn.close")
	if len(lines) > room {
		label = i18n.T("btn.close_scroll")
	}
	body = append(body, m.buttons(len(body)+1, []btn{{label, true, (*Model).closeOverlay}})...)
	return ovRender(body, w)
}

// ovRender cuts / pads the overlay content to one width before boxing. ⚠️ Cut first: a wrapped line shifts every click zone below it.
func ovRender(body []string, w int) string {
	for i := range body {
		body[i] = fit(body[i], w-4)
	}
	return ovBox.Width(w).Render(strings.Join(body, "\n"))
}

func (m *Model) renderResume() string {
	w := m.ovWidth()
	r := m.ov.rec
	inner := w - 4

	var body []string
	body = append(body, boldSty.Foreground(cText).Render(i18n.T("resume.title")))
	body = append(body, m.titleLines(inner, len(body)+1)...)
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	body = append(body, accent.Render(i18n.T("resume.target")))
	for _, l := range render.Wrap(m.resumeTargetLine(r), inner) {
		body = append(body, l)
	}
	body = append(body, "")

	p := m.ov.plan
	if p.Live.TabID != "" {
		body = append(body, okSty.Render(render.GlyphOK+i18n.T("resume.note_running")))
	} else if p.Spec.Exec == "" {
		body = append(body, errSty.Render(render.GlyphErr+i18n.T("resume.note_no_session")))
	} else {
		if p.Live.BackgroundID != "" {
			body = append(body, warnSty.Render(render.GlyphWarn+i18n.T("resume.note_background")))
		}
		var trouble []string
		for _, c := range p.Checks {
			if c.OK {
				body = append(body, okSty.Render(render.GlyphOK+" "+render.Truncate(c.Text, inner-2)))
				continue
			}
			g := render.GlyphWarn
			if !c.Warn {
				g = render.GlyphErr
			}
			trouble = append(trouble, render.Wrap(g+" "+c.Text, inner-4)...)
		}
		if len(trouble) > 0 {
			body = append(body, strings.Split(warnBox.Width(inner-2).Render(strings.Join(trouble, "\n")), "\n")...)
		}
		line := p.Spec.Display()
		if m.ov.app {
			line = capture.AppURL(r)
		}
		body = append(body, "", dimmed.Render(render.Truncate(line, inner)))
	}
	body = append(body, "")

	body = append(body, m.buttonGroups(len(body)+1, m.resumeGroups())...)
	return ovRender(body, w)
}

// resumeGroups are the resume dialog's buttons: where to continue, the project folder, the record; the overlay width
// is computed from them.
func (m *Model) resumeGroups() []btnGroup {
	p := m.ov.plan
	project := btnGroup{label: i18n.T("resume.group.project"), bs: []btn{{"i IDE", false, (*Model).openIDE}, {"c VS Code", false, (*Model).openCode}, {"o " + fileManagerName(), false, (*Model).openFiles}}}
	cancel := btn{i18n.T("btn.cancel"), false, (*Model).closeOverlay}
	if dirGone, tGone := m.broken(m.ov.rec); dirGone || tGone { // unrecoverable: move / delete instead of resume
		gs := []btnGroup{{label: i18n.T("resume.group.repair"), bs: []btn{{i18n.T("resume.btn_move"), dirGone, (*Model).askMove}, {i18n.T("resume.btn_delete"), !dirGone, (*Model).askDelete}}}}
		if !dirGone {
			gs = append(gs, project)
		}
		gs[len(gs)-1].end = []btn{cancel}
		// favorite / done / archive / edit; move and delete are already there
		return append(gs, btnGroup{label: i18n.T("resume.group.record"), bs: m.recordBtns()[:4]})
	}
	primary := i18n.T("resume.btn_resume")
	switch {
	case p.Live.TabID != "":
		primary = i18n.T("resume.btn_switch")
	case p.Live.BackgroundID != "":
		primary = i18n.T("resume.btn_attach")
	}
	if m.ov.app {
		primary = i18n.T("resume.btn_resume_r")
	}
	bs := []btn{
		{primary, !m.ov.app, func(mm *Model) { mm.doResume(false) }},
		{i18n.T("resume.btn_terminal"), false, func(mm *Model) { mm.doResume(true) }},
	}
	if r := m.ov.rec; appReady(r) {
		label := i18n.F("resume.btn_app", capture.AppName(r.Provider))
		if m.ov.app {
			label = i18n.F("resume.btn_app_enter", capture.AppName(r.Provider))
		}
		app := btn{label, m.ov.app, (*Model).doApp}
		if m.ov.app {
			bs = append([]btn{app}, bs...)
		} else {
			bs = append(bs, app)
		}
	}
	if m.resumeCommand() != "" {
		bs = append(bs, btn{i18n.T("resume.btn_copy"), false, (*Model).copyResume})
	}
	project.end = []btn{cancel}
	return []btnGroup{
		{label: i18n.T("resume.group.resume"), bs: bs},
		project,
		{label: i18n.T("resume.group.record"), bs: m.recordBtns()},
	}
}

// appReady: r's desktop app was found. The lookup runs in the background when the resume dialog first opens; the
// button appears once it answers.
func appReady(r *fav.Rec) bool {
	ok, _ := capture.AppKnown(r.Provider)
	return ok && capture.AppURL(r) != ""
}

type appProbedMsg struct{}

// probeApp looks up r's desktop app unless that is already known.
func probeApp(r *fav.Rec) tea.Cmd {
	if _, known := capture.AppKnown(r.Provider); known || capture.AppURL(r) == "" {
		return nil
	}
	p := r.Provider
	return func() tea.Msg {
		capture.AppAvailable(p)
		return appProbedMsg{}
	}
}

// appFirst: the resume dialog leads with the desktop app — the setting says so (always, or for sessions started there)
// and the session is not running in a Herdr tab.
func (m *Model) appFirst(r *fav.Rec, p capture.Plan) bool {
	if !appReady(r) || p.Live.TabID != "" {
		return false
	}
	return m.cfg.ResumeIn == fav.ResumeApp || m.cfg.ResumeIn == fav.ResumeOrigin && r.App
}

// doApp hands the session to its desktop app. A session running in a terminal is not opened a second time there.
func (m *Model) doApp() {
	r := m.ov.rec
	if r.ID != "" {
		if cur := m.store.Get(r.ID); cur != nil {
			r = cur
		}
	}
	if m.isLive(r.SessionID) && !r.App {
		m.flash(i18n.T("resume.app_live"))
		return
	}
	m.ov = overlay{}
	m.flash(i18n.F("resume.opening_app", capture.AppName(r.Provider)))
	m.pending = func() tea.Msg { return appDoneMsg{r, capture.OpenApp(r)} }
}

type appDoneMsg struct {
	rec *fav.Rec
	err error
}

// recordBtns: the letter-key actions also get buttons, reachable by Enter → ← → Enter when an IME eats letters.
func (m *Model) recordBtns() []btn {
	r := m.ov.rec
	pick := func(cond bool, on, off string) string {
		if cond {
			return i18n.T(on)
		}
		return i18n.T(off)
	}
	act := func(f func(*Model)) func(*Model) {
		return func(mm *Model) { mm.closeOverlay(); f(mm) }
	}
	return []btn{
		{pick(r != nil && r.Favorite(), "footer.unfavorite", "footer.favorite"), false, act((*Model).toggleFavorite)},
		{pick(r != nil && r.Done(), "footer.undo_done", "footer.done"), false, act(func(mm *Model) { mm.toggleStatus(fav.StatusDone) })},
		{pick(r != nil && r.Archived(), "footer.unarchive", "footer.archive"), false, act((*Model).toggleArchive)},
		{i18n.T("footer.edit"), false, func(mm *Model) { mm.pending = mm.openEdit() }},
		{i18n.T("footer.move"), false, (*Model).askMove},
		{i18n.T("footer.delete"), false, (*Model).askDelete},
	}
}

func (m *Model) resumeCommand() string {
	p := m.ov.plan
	if p.Spec.Exec == "" {
		return ""
	}
	return p.Spec.ShellLine()
}

func (m *Model) copyResume() {
	cmd := m.resumeCommand()
	if cmd == "" {
		m.flash(i18n.T("resume.nothing_to_copy"))
		return
	}
	if err := copyText(cmd); err != nil {
		m.flash(cmd)
		return
	}
	m.closeOverlay()
	m.flash(i18n.T("resume.copied") + cmd)
}

func (m *Model) titleLines(inner, y0 int) []string {
	if !m.ov.editing {
		hint := dimmed.Render(i18n.T("resume.btn_edit_title"))
		text := render.Truncate(m.ov.edit.Value(), inner-render.Width(i18n.T("resume.btn_edit_title"))-2)
		line := text + strings.Repeat(" ", max(1, inner-render.Width(text)-render.Width(i18n.T("resume.btn_edit_title")))) + hint
		m.mark(y0, ovPad, inner, (*Model).editTitle)
		return []string{line}
	}
	m.ov.edit.Width = inner - 6
	m.mark(y0+1, ovPad, inner, func(mm *Model) { mm.placeCursor(&mm.ov.edit, 2) })
	box := strings.Split(panelSty.BorderForeground(cAccent).Width(inner-2).Render(fit(m.ov.edit.View(), inner-4)), "\n")
	return append(box, dimmed.Render(i18n.T("resume.edit_hint")))
}

func (m *Model) editTitle() {
	m.ov.editing = true
	m.ov.edit.Focus()
	m.ov.edit.CursorEnd()
}

// doResume persists an edited title before resuming.
func (m *Model) doResume(noHerdr bool) {
	r := m.ov.rec
	if r.ID != "" {
		if cur := m.store.Get(r.ID); cur != nil { // ov.rec is stale after a store reload
			r = cur
		}
	}
	if t := strings.TrimSpace(m.ov.edit.Value()); t != "" && t != r.Title {
		r.Title = t
		if r.ID != "" {
			if err := m.store.Put(r); err != nil {
				m.flash(i18n.T("resume.title_not_saved") + err.Error())
			}
		}
	}
	p := m.ov.plan
	if noHerdr {
		var err error
		if p, err = capture.PlanResume(r, m.live, true); err != nil {
			m.flash(err.Error())
			return
		}
	}
	if p.Live.TabID == "" && p.Ws == nil {
		// resuming in this terminal execs over it: quit the TUI and let the caller do it
		m.finish(Result{Resume: r, NoHerdr: noHerdr})
		return
	}
	if c := p.Blocking(); c != nil && p.Live.TabID == "" { // focusing a tab touches no session file; checks do not apply
		m.flash(i18n.T("resume.failed") + c.Text)
		return
	}
	m.ov = overlay{}
	if p.Live.TabID != "" {
		m.flash(i18n.T("resume.switching"))
	} else {
		m.flash(i18n.F("resume.opening_tab", p.Ws.Label))
	}
	m.pending = func() tea.Msg {
		msg, warn, err := p.RunInHerdr(r)
		return herdrDoneMsg{rec: r, msg: msg, focused: p.Live.TabID != "", warn: warn, err: err}
	}
}

func (m *Model) resumeTargetLine(r *fav.Rec) string {
	if m.ov.app {
		return i18n.F("resume.where.app", capture.AppName(r.Provider))
	}
	return m.ov.plan.Target(r, "  "+render.GlyphArrow+"  ")
}

func providerLabel(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude Code"
	case fav.ProviderCodex:
		return "Codex CLI"
	}
	return p
}

func overlayOrigin(boxLines []string, w, h int) (int, int) {
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, ansi.StringWidth(l))
	}
	return max(0, (w-bw)/2), max(0, (h-len(boxLines))/2)
}

// composite lays the overlay over the base frame: the base is stripped of colour and dimmed, then cut per column.
func composite(base, boxLines []string, x, y, w, h int) string {
	out := make([]string, len(base))
	for i, l := range base {
		out[i] = backSty.Render(ansi.Strip(l))
	}
	for i, bl := range boxLines {
		row := y + i
		if row < 0 || row >= len(out) {
			continue
		}
		plain := ansi.Strip(base[min(row, len(base)-1)])
		line := backSty.Render(fit(plain, x)) + bl
		if tail := x + ansi.StringWidth(bl); tail < w {
			line += backSty.Render(strings.Repeat(" ", w-tail))
		}
		out[row] = line
	}
	return strings.Join(out, "\n")
}

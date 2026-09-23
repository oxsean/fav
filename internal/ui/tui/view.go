package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.reuseFrame && m.lastFrame != "" {
		m.reuseFrame = false
		return m.paintSelection(m.lastFrame)
	}
	m.reuseFrame = false
	m.zones = nil
	t0 := time.Now()
	defer func() {
		if d := time.Since(t0); d > 30*time.Millisecond {
			tracef("slow view %dms", d.Milliseconds())
		}
	}()
	lines := m.baseLines()
	if !m.ov.active() {
		m.lastFrame = strings.Join(lines, "\n")
		return m.paintSelection(m.lastFrame)
	}

	// overlays cover the base frame's zones; overlay zones are registered box-local, then shifted
	m.zones = nil
	ovLines := strings.Split(m.renderOverlay(), "\n")
	x, y := overlayOrigin(ovLines, m.w, m.h)
	m.ovX, m.ovY, m.ovH = x, y, len(ovLines)
	m.ovW = 0
	for _, l := range ovLines {
		m.ovW = max(m.ovW, ansi.StringWidth(l))
	}
	m.shiftZones(0, x, y)
	m.lastFrame = composite(lines, ovLines, x, y, m.w, m.h)
	return m.paintSelection(m.lastFrame)
}

func (m *Model) baseLines() []string {
	var out []string
	if m.w < compactCols {
		out = append(out, m.search.View())
	} else {
		out = append(out, m.header())
		out = append(out, m.searchBox(len(out))...)
		out = append(out, m.chipRow(len(out))...)
	}
	out = append(out, m.body(len(out), m.h-chromeHeight(m.w, m.h))...)

	for len(out) < m.h-1 {
		out = append(out, "")
	}
	out = out[:max(0, m.h-1)]
	out = append(out, m.footer())

	// ⚠️ every line must be exactly terminal width, or overlay compositing shows through and wrapping breaks the frame
	for i := range out {
		out[i] = fit(out[i], m.w)
	}
	return out
}

func (m *Model) header() string {
	names := []string{i18n.T("view.favorites") + " " + strconv.Itoa(m.nFav), i18n.T("view.sessions") + " " + strconv.Itoa(m.nAll), i18n.T("label.projects") + " " + strconv.Itoa(m.nProj), "Agents " + strconv.Itoa(len(m.live))}
	var tabs string
	var widths []int
	for i, n := range names {
		t, sty := "  "+n+"  ", dimmed
		if view(i) == m.view {
			t, sty = tabOpenL+" "+n+" "+tabOpenR, accent.Bold(true)
		}
		tabs += sty.Render(t)
		widths = append(widths, ansi.StringWidth(t))
	}

	left := " " + accent.Bold(true).Render(render.GlyphBrand+" Fav") + dimmed.Render(" Session Manager")
	right := m.liveSummary()
	if right != "" {
		right += "  "
	}

	gap := m.w - ansi.StringWidth(left) - ansi.StringWidth(tabs) - ansi.StringWidth(right)
	if gap < 2 {
		return fit(left, m.w)
	}
	l := gap / 2
	if right != "" {
		m.mark(0, m.w-ansi.StringWidth(right), ansi.StringWidth(right), func(mm *Model) { mm.setView(viewLive) })
	}
	tabX := ansi.StringWidth(left) + l
	for i, wd := range widths {
		v := view(i)
		m.mark(0, tabX, wd, func(mm *Model) { mm.setView(v) })
		tabX += wd
	}

	return left + strings.Repeat(" ", l) + tabs + strings.Repeat(" ", gap-l) + right
}

func (m *Model) searchBox(y0 int) []string {
	sty := panelSty
	if m.typing {
		sty = panelSty.BorderForeground(cAccent)
	}
	lines := strings.Split(sty.Width(m.w-2).Render(fit(m.search.View(), m.w-4)), "\n")
	m.markRows(y0, 0, m.w, len(lines), func(mm *Model) { mm.focusSearch(); mm.placeCursor(&mm.search, 2) })
	return lines
}

func (m *Model) chipRow(y0 int) []string {
	cs := m.chipData()
	if chipRows(m.h) == 1 {
		var parts []string
		x := 0
		for i, c := range cs {
			s := c.icon + " " + c.label + " " + c.value
			sty := dimmed
			if c.on {
				sty = accent.Bold(true)
			}
			if i == m.chipFocus {
				sty = chipFoc.UnsetBorderStyle().UnsetPadding()
			}
			parts = append(parts, sty.Render(s))
			open := c.open
			m.mark(y0, x, ansi.StringWidth(s), func(mm *Model) { open(mm) })
			x += ansi.StringWidth(s) + 6
		}
		return []string{strings.Join(parts, frame.Render("  "+vBar+"  "))}
	}

	n := len(cs)
	base, extra := (m.w-(n-1))/n, (m.w-(n-1))%n
	cols := make([][]string, n)
	x := 0
	for i, c := range cs {
		w := base
		if i < extra {
			w++
		}
		sty := chipSty
		if c.on {
			sty = chipSel
		}
		if i == m.chipFocus {
			sty = chipFoc
		}
		// drops the text label and keeps icon and value when it does not fit
		text := c.icon + " " + c.label + " " + c.value
		if render.Width(text) > w-4 {
			text = c.icon + " " + c.value
		}
		cols[i] = strings.Split(sty.Width(w-2).Render(fit(text, w-4)), "\n")
		open := c.open
		m.markRows(y0, x, w, len(cols[i]), func(mm *Model) { open(mm) })
		x += w + 1
	}

	out := make([]string, 3)
	for i := range out {
		var b strings.Builder
		for j := range cols {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(at(cols[j], i))
		}
		out[i] = b.String()
	}
	return out
}

type chip struct {
	icon, label, value string
	on                 bool
	open               func(*Model)
}

func (m *Model) chipData() []chip {
	q := fav.Parse(m.search.Value())
	tagVal := i18n.T("label.all")
	if len(q.Tags) > 0 {
		tagVal = strings.Join(q.Tags, "+")
	}
	chips := []chip{
		{render.GlyphProject, i18n.T("label.projects"), orAll(q.Project), q.Project != "", (*Model).pickProjects},
		{render.GlyphTag, i18n.T("label.tags"), tagVal, len(q.Tags) > 0, (*Model).pickTags},
		{render.GlyphTerm, i18n.T("label.source"), orAll(q.Provider), q.Provider != "", (*Model).cycleProvider},
		{render.GlyphOK, i18n.T("label.status"), statusLabel(q.Status), q.Status != fav.StatusOpen, (*Model).pickStatus},
		{render.GlyphClock, i18n.T("label.time"), timeLabel(q), !q.After.IsZero() || !q.Before.IsZero(), (*Model).pickDate},
	}
	if m.view == viewLive {
		chips = append(chips[:3], chips[4])
	}
	return chips
}

func orAll(s string) string {
	if s == "" {
		return i18n.T("label.all")
	}
	return s
}

func statusLabel(s string) string {
	switch s {
	case "live":
		return "Agents"
	case "all":
		return i18n.T("label.all")
	case fav.StatusOpen:
		return i18n.T("status.open")
	case fav.StatusActive:
		return i18n.T("status.active")
	case "archived":
		return i18n.T("status.archived")
	case fav.StatusTrash:
		return i18n.T("status.trash")
	case fav.StatusAgent:
		return i18n.T("status.agent")
	}
	return render.StatusLabel(s)
}

func timeLabel(q fav.Query) string {
	if q.After.IsZero() && q.Before.IsZero() {
		return i18n.T("label.all")
	}
	switch {
	case !q.After.IsZero() && !q.Before.IsZero():
		return q.After.Format("01-02") + ".." + q.Before.AddDate(0, 0, -1).Format("01-02")
	case !q.After.IsZero():
		return i18n.T("date.from") + q.After.Format("01-02")
	}
	return i18n.T("date.until") + q.Before.Format("01-02")
}

func (m *Model) body(y0, h int) []string {
	if h < 1 {
		return nil
	}
	if m.w < compactCols {
		return m.listBlock(y0, 0, m.w, h)
	}
	if m.detail {
		return m.detailBlock(y0, 0, m.w, h)
	}

	listW := m.listWidth()
	if !m.twoColumn() {
		return m.listBlock(y0, 0, listW, h)
	}

	prevW := m.w - listW - 1
	left := m.listBlock(y0, 0, listW, h)
	right := m.detailBlock(y0, listW+1, prevW, h)

	out := make([]string, h)
	for i := range out {
		out[i] = fit(at(left, i), listW) + " " + fit(at(right, i), prevW)
	}
	return out
}

func (m *Model) listBlock(y0, x0, w, h int) []string {
	if m.view == viewProjects && m.w >= compactCols {
		return panel(m.listTitle(), m.listPane(y0+1, x0+2, w-4, h-2), w, h)
	}
	if m.w < compactCols {
		return m.listPane(y0, x0, w, h)
	}
	out := []string{fit(dimmed.Render(m.listTitle()), w)}
	return append(out, m.listPane(y0+1, x0, w, h-1)...)
}

func (m *Model) listTitle() string {
	n := m.countRecs()
	switch m.view {
	case viewProjects:
		return i18n.F("title.projects", m.nProj, n, projSortLabel(m.cfg.ProjectSort))
	case viewFavorites:
		return i18n.F("title.favorites", n, m.sortBy.label())
	case viewLive:
		how := i18n.T("title.live_by_start")
		switch m.cfg.LiveSort {
		case liveSortGroup:
			how = i18n.T("title.live_by_status")
		case liveSortActive:
			how = i18n.T("title.live_by_active")
		}
		return i18n.F("title.live", n, how)
	}
	return i18n.F("title.sessions", n, m.sortBy.label())
}

func panel(title string, content []string, w, h int) []string {
	// lipgloss Width() includes padding, not border: box w-2, content w-4
	body := make([]string, 0, h-2)
	for i := 0; i < h-2; i++ {
		body = append(body, fit(at(content, i), w-4))
	}
	lines := strings.Split(panelSty.Width(w-2).Height(h-2).Render(strings.Join(body, "\n")), "\n")

	if len(lines) > 0 && title != "" {
		lines[0] = titledTopBorder(title, w)
	}
	for i := range lines {
		lines[i] = fit(lines[i], w)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines[:h]
}

func titledTopBorder(title string, w int) string {
	label := " " + render.Truncate(title, max(0, w-8)) + " "
	fill := max(0, w-3-ansi.StringWidth(label)) // ╭ ─ label ─…─ ╮ is exactly w columns; one short and ╮ misaligns with the │ below
	return frame.Render("╭"+hRule) + accent.Render(label) +
		frame.Render(strings.Repeat(hRule, fill)+"╮")
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "")
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func at(ss []string, i int) string {
	if i < len(ss) {
		return ss[i]
	}
	return ""
}

// listLines flattens rows into screen lines and registers click actions; only rows in [from, to) are rendered, scrolling is by line.
func (m *Model) listLines(w, from, to int) (lines []string, acts []func(*Model)) {
	add := func(l string, a func(*Model)) { lines = append(lines, l); acts = append(acts, a) }
	hs, y := m.rowHeights(), 0

	for i, r := range m.rows {
		idx := i
		if y+hs[i] <= from || y >= to {
			for k := 0; k < hs[i]; k++ {
				add("", nil)
			}
			y += hs[i]
			continue
		}
		y += hs[i]
		if r.group != "" {
			var act func(*Model)
			if m.view == viewProjects {
				g := r.group
				act = func(mm *Model) { mm.cursor, mm.pane = idx, paneList; mm.open[g] = !mm.open[g]; mm.refresh() } // the cursor follows so the expanded children stay on screen
			}
			add(m.groupLine(r, w, i == m.cursor), act)
			continue
		}
		click := func(mm *Model) { mm.clickRow(idx) }
		if m.view == viewProjects || m.w < compactCols {
			add(m.recLine(r.rec, i == m.cursor, w), click)
			continue
		}
		for _, l := range m.cardBox(r.rec, i == m.cursor, w) {
			add(l, click)
		}
		add("", nil)
	}
	return
}

func (m *Model) listPane(y0, x0, w, h int) []string {
	if h < 1 {
		return nil
	}
	if len(m.rows) == 0 {
		return append([]string{dimmed.Render(i18n.T("list.empty"))}, blanks(h-1)...)
	}
	m.ensureVisible()
	all, acts := m.listLines(w, m.scroll, m.scroll+h)

	out := make([]string, h)
	for i := range out {
		j := m.scroll + i
		if j >= len(all) {
			continue
		}
		out[i] = all[j]
		m.mark(y0+i, x0, w, acts[j])
	}
	return out
}

func (m *Model) groupLine(r row, w int, sel bool) string {
	label := r.group
	sty := dimmed.Bold(true)
	if m.view == viewProjects {
		mark := render.GlyphOpen
		if r.folded {
			mark = render.GlyphClosed
		}
		label = mark + " " + label
		sty = accent.Bold(true)
		if sel {
			sty = selTitle
		}
	}
	// counts only in the projects view
	count := ""
	if m.view == viewProjects && r.count > 0 {
		count = strconv.Itoa(r.count)
	}
	text := sty.Render(render.Truncate(label, w-len(count)-2))
	gap := w - ansi.StringWidth(text) - len(count)
	if gap < 1 {
		return text
	}
	if m.view == viewProjects {
		if sel {
			return sty.Render(fit(render.Truncate(label, w-len(count)-2)+strings.Repeat(" ", gap)+count, w))
		}
		return text + strings.Repeat(" ", gap) + dimmed.Render(count)
	}
	return text + " " + frame.Render(strings.Repeat(hRule, gap-1))
}

func (m *Model) recLine(r *fav.Rec, sel bool, w int) string {
	when := render.When(m.when(r), m.now)
	turns := ""
	if r.Turns > 0 {
		turns = " " + i18n.F("card.turns", r.Turns)
	}
	mark := ""
	if d, t := m.broken(r); d || t {
		mark = " !"
	}
	title := render.Truncate(r.Title, w-render.Width(when)-render.Width(turns)-len(mark)-5)
	pad := strings.Repeat(" ", max(0, w-render.Width(when)-render.Width(turns)-render.Width(title)-len(mark)-5))
	tSty, nSty := plainSty, faint
	if sel {
		tSty, nSty = selTitle, faint.Background(cSelBg)
	}
	return fit(tSty.Render("  "+glyphFor(r)+" "+title)+brokenSty.Inherit(tSty).Render(mark)+nSty.Render(turns)+tSty.Render(pad+" "+when), w)
}

func (m *Model) cardBox(r *fav.Rec, sel bool, w int) []string {
	inner := w - 4
	when := render.When(m.when(r), m.now)
	meta := providerShort(r.Provider)
	if r.Project != "" {
		meta += "  ·  " + r.Project
	}
	if r.Turns > 0 {
		meta += "  ·  " + i18n.F("card.turns", r.Turns)
	}
	liveText, tone := "", 0
	if l, ok := m.liveOf(r); ok && m.view == viewLive { // live status only on the Agents page
		liveText, tone = liveLabel(l, m.now)
		meta += "  ·  " + liveText
	}
	if r.ID != "" && r.Status != fav.StatusDefault {
		meta += "  ·  " + render.Glyph(r.Status) + " " + render.StatusLabel(r.Status)
	}
	if r.Archived() {
		meta += "  ·  " + render.GlyphArchive + i18n.T("card.archived")
	}

	title := fit(glyphFor(r)+" "+r.Title, inner)
	mark := ""
	if d, t := m.broken(r); d || t {
		title, mark = fit(glyphFor(r)+" "+r.Title, inner-2)+" ", "!"
	}
	metaLine := fit(render.Pad(meta, max(0, inner-render.Width(when)-1))+" "+when, inner)
	tags := fit(tagString(r.Tags), inner)
	if !r.Favorite() && len(r.Tags) == 0 {
		tags = fit(shortenHome(r.Cwd), inner) // cwd when there are no tags
	}

	sty, tSty, mSty, gSty := cardSty, boldSty, tagSty, dimmed
	if sel {
		sty, tSty, mSty, gSty = cardSel, selTitle, selLine.Foreground(cTag), selLine.Foreground(cMuted)
	}
	metaOut := gSty.Render(metaLine)
	if a, b, ok := strings.Cut(metaLine, liveText); ok && liveText != "" {
		metaOut = gSty.Render(a) + liveTone[tone].Inherit(gSty).Render(liveText) + gSty.Render(b)
	}
	content := tSty.Render(title) + brokenSty.Inherit(tSty).Render(mark) + "\n" + metaOut + "\n" + mSty.Render(tags)
	return strings.Split(sty.Width(w-2).Render(content), "\n")
}

func tagString(tags []string) string {
	var b strings.Builder
	for i, t := range tags {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('#')
		b.WriteString(t)
	}
	return b.String()
}

func providerShort(p string) string {
	switch p {
	case fav.ProviderClaude:
		return "Claude"
	case fav.ProviderCodex:
		return "Codex"
	}
	return p
}

func glyphFor(r *fav.Rec) string {
	if r.Favorite() {
		return render.GlyphActive
	}
	return render.GlyphSession
}

func (m *Model) detailBlock(y0, x0, w, h int) []string {
	r := m.current()
	if r == nil {
		if g := m.groupUnderCursor(); m.view == viewProjects && g != "" {
			return m.projectBlock(g, y0, x0, w, h)
		}
		return panel(i18n.T("detail.preview"), nil, w, h)
	}
	m.chatY, m.chatX = -1, x0+2
	inner := w - 4

	target := m.targetBox(inner)
	checks := m.checkLines(r, inner)
	avail := h - 2 - len(target) - len(checks)

	var body []string
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(r.Title, inner)), "")
	body = append(body, m.statusLine(r, inner))
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	if r.Summary != "" {
		label := i18n.T("card.summary")
		if r.ID == "" {
			label = i18n.T("detail.first_message") // unfavorited sessions have no summary: show the first prompt
		}
		body = append(body, accent.Render(label))
		for _, l := range render.Wrap(r.Summary, inner) {
			body = append(body, l)
		}
		body = append(body, "")
	}
	body = append(body, m.fieldLines(r, inner)...)
	chatStart := len(body)
	chat, owners := m.chatLines(r, inner, avail-len(body))
	body = append(body, chat...)
	if m.chat.typing && chatStart+1 < avail {
		m.mark(y0+1+chatStart+1, x0+2+m.chatInputX, inner-m.chatInputX, func(mm *Model) { mm.placeCursor(&mm.chat.input, 0) })
	}
	for i, owner := range owners {
		if owner >= 0 && chatStart+i < avail {
			idx := owner
			m.mark(y0+1+chatStart+i, x0+2, inner, func(mm *Model) { mm.clickChat(idx) }) // top border 1 row, left border + padding 2 columns
		}
	}

	if len(body) > avail && avail > 0 {
		body = body[:avail]
		body[avail-1] = dimmed.Render(i18n.T("detail.more_below"))
	}
	for len(body) < avail {
		body = append(body, "")
	}
	body = append(body, checks...)
	body = append(body, target...)
	return panel("", body, w, h)
}

func (m *Model) statusLine(r *fav.Rec, w int) string {
	state := accent.Render(render.GlyphActive + i18n.T("detail.favorited"))
	if !r.Favorite() {
		state = dimmed.Render(render.GlyphSession + i18n.T("detail.not_favorited"))
	}
	if r.Done() {
		state += dimmed.Render("  " + render.GlyphDone + i18n.T("detail.done"))
	} else {
		state += okSty.Render("  " + render.StatusLabel(r.Status))
	}
	if r.Archived() {
		state += dimmed.Render("  " + render.GlyphArchive + i18n.T("detail.archived"))
	}
	if r.PinnedPath != "" {
		state += accent.Render("  " + render.GlyphPinned + i18n.T("detail.pinned"))
	}
	if l, ok := m.liveOf(r); ok {
		text, tone := liveLabel(l, m.now)
		state += liveTone[tone].Render("  " + text)
		switch {
		case l.TabID != "":
			state += dimmed.Render(i18n.T("detail.live_herdr"))
		case l.BackgroundID != "":
			state += dimmed.Render(i18n.F("detail.live_attach", l.BackgroundID))
		default:
			state += dimmed.Render(i18n.T("detail.live_elsewhere"))
		}
	}
	tail := "  ·  " + providerLabel(r.Provider) + "  ·  " + render.WhenFull(r.When())
	return fit(state+dimmed.Render(tail), w)
}

func (m *Model) fieldLines(r *fav.Rec, w int) []string {
	const labelW = 12
	var out []string
	add := func(k, v string) {
		if v != "" {
			out = append(out, dimmed.Render(render.Pad(k, labelW))+render.Truncate(v, w-labelW))
		}
	}
	add(render.GlyphProject+i18n.T("card.project"), r.Project)
	add(render.GlyphTerm+i18n.T("card.type"), r.WorkType)
	add(render.GlyphBranch+i18n.T("card.branch"), r.GitBranch)
	add(render.GlyphDir+i18n.T("card.directory"), shortenHome(r.Cwd))
	add(render.GlyphSession+i18n.T("card.session"), r.SessionID)
	if r.Turns > 0 {
		add(render.GlyphClock+i18n.T("card.turns_label"), i18n.F("card.turns_last_write", r.Turns, render.WhenFull(r.LastAt)))
	}
	if r.HerdrWorkspace != "" {
		herdr := r.HerdrWorkspace
		if r.HerdrTab != "" {
			herdr += "  " + render.GlyphArrow + "  " + r.HerdrTab
		}
		add(render.GlyphHerdr+" Herdr", herdr)
	}
	add(render.GlyphTag+i18n.T("card.tags"), tagString(r.Tags))
	if l, ok := m.live[r.SessionID]; ok {
		where := i18n.T("detail.background")
		if l.TabID != "" {
			where = "Herdr tab " + l.TabID
		}
		add(render.GlyphLive+i18n.T("live.suffix_running"), where)
	}
	return out
}

func (m *Model) chatLines(r *fav.Rec, w, room int) (lines []string, owners []int) {
	p := m.probes[r]
	if p == nil || !p.done || len(p.msgs) == 0 || room < 5 {
		return nil, nil
	}
	if m.chatRec != r {
		if m.chatRec != nil {
			m.chatScroll, m.chatSkip, m.chatCur = 0, 0, 0
		}
		m.chatRec = r
	}
	m.chatScroll = min(max(m.chatScroll, 0), len(p.msgs)-1)
	m.chatCur = min(max(m.chatCur, 0), len(p.msgs)-1)
	m.chatW, m.chatRoom = w, room
	q := m.chat.query()

	// keyboard moved the highlight: the viewport follows it; wheel moved the viewport: the highlight is clamped into view
	var out []string
	var own []int
	shown := 0
	for {
		out, own, shown = m.layoutChat(p, w, room, q)
		last := m.chatScroll + shown - 1
		if !m.chatFollow {
			m.chatCur = min(max(m.chatCur, m.chatScroll), last)
			break
		}
		if m.chatCur < m.chatScroll {
			m.chatScroll, m.chatSkip = m.chatCur, 0
			continue
		}
		if m.chatCur > last && m.chatScroll < m.chatCur {
			m.chatScroll, m.chatSkip = m.chatScroll+1, 0
			continue
		}
		break
	}
	m.chatFollow = false
	m.chatShown = shown
	if shown == 0 {
		return nil, nil
	}
	pos := i18n.F("chat.range", m.chatScroll+1, m.chatScroll+shown, len(p.msgs))
	var tail string
	switch {
	case m.chat.typing:
		tail = m.chat.input.View()
	case q != "":
		hs := m.hits()
		if len(hs) == 0 {
			tail = i18n.F("chat.find_none", q)
		} else {
			tail = i18n.F("chat.find_hits", q, m.chat.cur+1, len(hs))
		}
	case p.loading && len(p.msgs) == 0:
		tail = i18n.T("chat.loading")
	case !p.full:
		tail = i18n.F("chat.loaded", len(p.msgs))
		if r.Msgs > len(p.msgs) {
			tail += i18n.F("chat.loaded_total", r.Msgs)
		}
		tail += i18n.T("chat.hint_more")
	default:
		tail = i18n.F("chat.hint_full", len(p.msgs))
	}
	if len(p.fresh) > 0 {
		pos += i18n.F("chat.new_msgs", len(p.fresh))
	}
	title := i18n.T("chat.title_prefix") + pos + " · " + tail + " "
	if m.chat.typing {
		m.chatInputX = 2 + ansi.StringWidth(i18n.T("chat.title_prefix")+pos+" · ") // relative to the chat area's left edge; detailBlock registers the zone
	}
	head := frame.Render(hRule+hRule) + dimmed.Render(title) + frame.Render(strings.Repeat(hRule, max(0, w-ansi.StringWidth(title)-2)))
	return append([]string{"", head}, out[:len(out)-1]...), append([]int{-1, -1}, own[:len(own)-1]...)
}

// layoutChat lays messages from the viewport position until room runs out, tagging each line with its message (-1 = blank); at least one message.
func (m *Model) layoutChat(p *probe, w, room int, q string) (out []string, own []int, shown int) {
	for i := m.chatScroll; i < len(p.msgs); i++ {
		block := m.chatBlock(p.msgs[i], w, q, i == m.chatCur && m.pane == paneChat)
		if i == m.chatScroll {
			m.chatSkip = min(max(m.chatSkip, 0), len(block)-1)
			block = block[m.chatSkip:]
			if len(block)+2 > room {
				block = block[:room-2]
			}
		} else if len(out)+2+len(block) > room {
			break
		}
		for range block {
			own = append(own, i)
		}
		out = append(out, block...)
		shown++
	}
	return out, own, shown
}

func (m *Model) chatBlock(msg capture.Message, w int, q string, sel bool) []string {
	who, sty := i18n.T("chat.you"), boldSty.Foreground(cAccent)
	if msg.Role != "user" {
		who, sty = "AI", boldSty.Foreground(cMuted)
	}
	meta := "  ·  " + msg.At.Format("01-02 15:04")
	count := "  " + render.GlyphChars + " " + render.Chars(msg.Chars)
	head := sty.Render(who) + dimmed.Render(meta) + faint.Render(count)
	line := func(l string) string { return "  " + highlight(l, q) }
	if sel {
		hint := i18n.T("chat.hint_enter")
		head = selTitle.Render(who+meta) + faint.Inherit(selTitle).Render(count) + selTitle.Render(fit(hint, max(0, w-ansi.StringWidth(who+meta+count))))
		line = func(l string) string { return highlightWith(fit("  "+l, w), q, selBody, hitSty.Background(cSelBg)) }
	}
	out := []string{head}
	body := render.Wrap(plainText(msg.Text), w-2)
	if len(body) > chatBodyRows {
		body = body[:chatBodyRows]
		last := &body[chatBodyRows-1]
		if *last = render.Truncate(*last, w-4); !strings.HasSuffix(*last, "…") {
			*last += " …"
		}
	}
	for _, l := range body {
		out = append(out, line(l))
	}
	return append(out, "")
}

func plainText(s string) string {
	return strings.NewReplacer("**", "", "`", "").Replace(s)
}

func (m *Model) checkLines(r *fav.Rec, w int) []string {
	p := m.probes[r]
	if p == nil || !p.done {
		return []string{dimmed.Render(render.GlyphClock + i18n.T("detail.checking"))}
	}
	var out []string
	for _, c := range p.checks {
		switch {
		case c.OK:
			out = append(out, okSty.Render(render.GlyphOK+" "+render.Truncate(c.Text, w-2)))
		case c.Warn:
			out = append(out, warnSty.Render(render.GlyphWarn+" "+render.Truncate(c.Text, w-2)))
		default:
			out = append(out, errSty.Render(render.GlyphErr+" "+render.Truncate(c.Text, w-2)))
		}
	}
	return out
}

func (m *Model) targetBox(w int) []string {
	r := m.current()
	if r == nil || w < 12 {
		return nil
	}
	// ⚠️ content cut at w-4 (lipgloss Width includes padding, not border); two more columns wrap
	content := accent.Render(i18n.T("card.resume_target")) + "\n" + fit(render.Truncate(m.resumeTargetLine(r), w-4), w-4)
	return strings.Split(panelSty.Width(w-2).Render(content), "\n")
}

func (m *Model) footer() string {
	if m.notice != "" {
		return fit(accent.Render(render.Truncate(m.notice, m.w)), m.w)
	}
	var keys []string
	switch {
	case m.typing:
		keys = []string{i18n.T("footer.esc_typing"), i18n.T("footer.select_record"), i18n.T("footer.enter_typing")}
	case m.detail:
		keys = append([]string{i18n.T("footer.esc_back")}, m.sessionKeys(m.current())...)
	case m.w < compactCols:
		keys = []string{i18n.T("footer.enter_details"), i18n.T("footer.search"), i18n.T("footer.help"), i18n.T("footer.quit")}
	case !m.twoColumn(): // single-pane list: Enter opens the detail, Space does nothing
		keys = []string{i18n.T("footer.enter_details")}
		for _, k := range m.sessionKeys(m.current())[1:] {
			if k != i18n.T("footer.space_resume") {
				keys = append(keys, k)
			}
		}
	case m.chipFocus >= 0:
		keys = []string{i18n.T("footer.chip_switch"), i18n.T("footer.chip_open"), i18n.T("footer.chip_back")}
	case m.projectFocus():
		keys = []string{i18n.T("footer.select_session"), i18n.T("footer.enter_jump"), i18n.T("footer.back_to_list"), i18n.T("footer.help")}
	case m.pane == paneChat:
		keys = []string{i18n.T("footer.select_message"), i18n.T("footer.full_text"), i18n.T("footer.copy"), i18n.T("footer.find"), i18n.T("footer.jump"), i18n.T("footer.back_to_list"), i18n.T("footer.help")}
	case m.view == viewProjects && m.current() == nil:
		keys = []string{i18n.T("footer.enter_expand"), i18n.T("footer.space_expand"), i18n.T("footer.fold_all"), i18n.T("footer.move_project"), i18n.T("footer.search"), i18n.T("footer.tab"), i18n.T("footer.help")}
	case m.inTrash():
		keys = []string{i18n.T("footer.restore"), i18n.T("footer.status"), i18n.T("footer.search"), i18n.T("footer.tab"), i18n.T("footer.help")}
	default:
		keys = m.sessionKeys(m.current())
	}
	count := "" // compact mode has no list title: the count goes to the footer
	if m.w < compactCols {
		count = dimmed.Render(i18n.F("footer.items", m.countRecs()))
	}
	for len(keys) > 1 {
		hint := renderKeys(keys)
		if gap := m.w - ansi.StringWidth(hint) - ansi.StringWidth(count); gap >= 1 {
			return hint + strings.Repeat(" ", gap) + count
		}
		keys = keys[:len(keys)-1]
	}
	return fit(renderKeys(keys), m.w)
}

// sessionKeys is the footer for a selected session, the same in all three views: actions → record state → edit / move / delete →
// view-specific → navigation; cut from the end when narrow, so common keys come first.
func (m *Model) sessionKeys(r *fav.Rec) []string {
	pick := func(cond bool, on, off string) string {
		if cond {
			return i18n.T(on)
		}
		return i18n.T(off)
	}
	keys := []string{i18n.T("footer.enter_actions")}
	switch {
	case m.view == viewLive:
		keys = append(keys, i18n.T("footer.space_switch"), i18n.T("footer.close_tab"))
	default:
		if d, t := m.broken(r); !d && !t {
			keys = append(keys, i18n.T("footer.space_resume"))
		}
	}
	keys = append(keys, pick(r != nil && r.Favorite(), "footer.unfavorite", "footer.favorite"))
	if m.view != viewLive {
		keys = append(keys,
			pick(r != nil && r.Done(), "footer.undo_done", "footer.done"),
			pick(r != nil && r.Archived(), "footer.unarchive", "footer.archive"))
	}
	keys = append(keys, i18n.T("footer.edit"))
	if m.view != viewLive {
		keys = append(keys, i18n.T("footer.move"), i18n.T("footer.delete"))
	}
	if m.view == viewProjects {
		keys = append(keys, i18n.T("footer.fold_all"))
	}
	return append(keys, i18n.T("footer.search"), i18n.T("footer.tab"), i18n.T("footer.help"))
}

func renderKeys(keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		key, desc, ok := strings.Cut(k, " ")
		if !ok {
			parts[i] = dimmed.Render(k)
			continue
		}
		parts[i] = accent.Render(key) + dimmed.Render(" "+desc)
	}
	return strings.Join(parts, frame.Render("  "+vBar+"  "))
}

// countRecs counts records under the current filter; collapsed groups count from their header.
func (m *Model) countRecs() int {
	n := 0
	for _, r := range m.rows {
		switch {
		case r.rec != nil:
			n++
		case r.folded:
			n += r.count
		}
	}
	return n
}

func blanks(n int) []string {
	if n <= 0 {
		return nil
	}
	return make([]string, n)
}

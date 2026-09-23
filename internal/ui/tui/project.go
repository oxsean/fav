package tui

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

// projectFocus: cursor on a group header with the right pane focused; ↑↓ picks a session, Enter jumps to it.
func (m *Model) projectFocus() bool {
	return m.view == viewProjects && m.pane == paneChat && m.current() == nil && m.chatVisible()
}

func (m *Model) jumpProject() {
	g := m.groupUnderCursor()
	recs := m.groups[g]
	if len(recs) == 0 {
		return
	}
	m.projCur = min(max(m.projCur, 0), len(recs)-1)
	m.jumpTo(g, recs[m.projCur])
	m.pane = paneList
}

// projectBlock is the right-pane project info when the cursor is on a group header; the session list is selectable when the pane has focus.
func (m *Model) projectBlock(name string, y0, x0, w, h int) []string {
	recs := m.groups[name]
	inner := w - 4
	const labelW = 12
	var body []string
	add := func(k, v string) {
		if v != "" {
			body = append(body, dimmed.Render(render.Pad(k, labelW))+render.Truncate(v, inner-labelW))
		}
	}
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(name, inner)), "")

	dirs := topN(recs, func(r *fav.Rec) string { return r.Cwd }, 1)
	if len(dirs) > 0 {
		dir := paths.Tilde(dirs[0].key)
		if st, err := os.Stat(dirs[0].key); err != nil || !st.IsDir() {
			dir += errSty.Render(i18n.T("project.missing"))
		}
		if extra := distinct(recs, func(r *fav.Rec) string { return r.Cwd }) - 1; extra > 0 {
			dir = i18n.F("project.dirs_more", dir, extra)
		}
		add(render.GlyphDir+i18n.T("card.directory"), dir)
	}
	for _, r := range recs {
		if r.GitRemote != "" {
			add(render.GlyphBranch+i18n.T("project.remote"), r.GitRemote)
			break
		}
	}
	if bs := topN(recs, func(r *fav.Rec) string { return r.GitBranch }, 4); len(bs) > 0 {
		names := make([]string, len(bs))
		for i, b := range bs {
			names[i] = b.key
		}
		add(render.GlyphBranch+i18n.T("card.branch"), strings.Join(names, " · "))
	}

	favs, claude, codex, turns, active, done, archived, live := 0, 0, 0, 0, 0, 0, 0, 0
	var first, last time.Time
	for _, r := range recs {
		if r.Favorite() {
			favs++
		}
		switch r.Provider {
		case fav.ProviderClaude:
			claude++
		case fav.ProviderCodex:
			codex++
		}
		turns += r.Turns
		switch {
		case r.Archived():
			archived++
		case r.Done():
			done++
		default:
			active++
		}
		if m.isLive(r.SessionID) {
			live++
		}
		if t := r.When(); !t.IsZero() && (first.IsZero() || t.Before(first)) {
			first = t
		}
		if t := m.when(r); t.After(last) {
			last = t
		}
	}
	add(render.GlyphSession+i18n.T("project.sessions_label"), i18n.F("project.sessions", len(recs), favs))
	add(render.GlyphTerm+" "+i18n.T("label.source"), i18n.F("project.providers", claude, codex))
	add(render.GlyphClock+i18n.T("card.turns_label"), i18n.F("card.turns", turns))
	add(render.GlyphOK+" "+i18n.T("label.status"), i18n.F("project.status", active, done, archived))
	if !first.IsZero() {
		add(render.GlyphClock+" "+i18n.T("label.time"), render.WhenFull(first)+"  "+render.GlyphArrow+"  "+render.When(last, m.now))
	}
	if live > 0 {
		add(render.GlyphLive+i18n.T("live.suffix_running"), i18n.F("project.live", live))
	}
	if hot := m.hotFiles(recs); len(hot) > 0 {
		base := ""
		if len(dirs) > 0 {
			base = dirs[0].key
		}
		add(render.GlyphEdit+i18n.T("project.hot_files"), render.FileList(hot, base))
	}
	if tags := topN(recs, nil, 8); len(tags) > 0 {
		parts := make([]string, len(tags))
		for i, t := range tags {
			parts[i] = "#" + t.key
		}
		add(render.GlyphTag+i18n.T("card.tags"), strings.Join(parts, " "))
	}

	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	body = append(body, accent.Render(i18n.T("project.recent")))
	room := h - 2 - len(body)
	focus := m.projectFocus()
	if focus {
		m.projCur = min(max(m.projCur, 0), len(recs)-1)
	}
	rows := room // the last line becomes "… N more" when it does not fit; a single line shows only the highlighted one
	if len(recs) > room && room > 1 {
		rows = room - 1
	}
	start := 0
	if focus && m.projCur >= rows { // page down when the highlight leaves the visible area
		start = m.projCur - rows + 1
	}
	for i := start; i < len(recs); i++ {
		r := recs[i]
		if i-start >= rows {
			if rows < room {
				body = append(body, dimmed.Render(i18n.F("project.more", len(recs)-i)))
			}
			break
		}
		when := render.When(m.when(r), m.now)
		title := render.Truncate(r.Title, inner-render.Width(when)-4)
		m.mark(y0+1+len(body), x0+2, inner, func(mm *Model) { mm.jumpTo(name, r) })
		line := "  " + glyphFor(r) + " " + title + strings.Repeat(" ", max(0, inner-render.Width(when)-render.Width(title)-4))
		if focus && i == m.projCur {
			body = append(body, selTitle.Render(line)+dimmed.Background(cSelBg).Render(when))
		} else {
			body = append(body, line+dimmed.Render(when))
		}
	}
	return panel(i18n.T("detail.project"), body, w, h)
}

func (m *Model) jumpTo(group string, r *fav.Rec) {
	m.open[group] = true
	m.refresh()
	for i, row := range m.rows {
		if row.rec == r {
			m.cursor = i
			break
		}
	}
}

type keyCount struct {
	key string
	n   int
}

// topN: the n most frequent; key nil counts tags.
func topN(recs []*fav.Rec, key func(*fav.Rec) string, n int) []keyCount {
	counts := map[string]int{}
	var order []string
	bump := func(k string) {
		if k == "" {
			return
		}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	for _, r := range recs {
		if key == nil {
			for _, t := range r.Tags {
				bump(t)
			}
		} else {
			bump(key(r))
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	out := make([]keyCount, 0, n)
	for _, k := range order {
		if len(out) == n {
			break
		}
		out = append(out, keyCount{k, counts[k]})
	}
	return out
}

func distinct(recs []*fav.Rec, key func(*fav.Rec) string) int {
	seen := map[string]bool{}
	for _, r := range recs {
		if k := key(r); k != "" {
			seen[k] = true
		}
	}
	return len(seen)
}

const (
	projSortActive = "active"
	projSortCount  = "count"
	projSortName   = "name"
)

var projSorts = []string{projSortActive, projSortCount, projSortName}

func projSortLabel(name string) string {
	switch name {
	case projSortCount:
		return i18n.T("sort.count")
	case projSortName:
		return i18n.T("sort.name")
	}
	return i18n.T("sort.active")
}

// orderGroups: the projects view orders groups by their newest record (the record order), by how many sessions they hold, or by name.
func orderGroups(names []string, by map[string][]*fav.Rec, mode string) []string {
	switch mode {
	case projSortCount:
		sort.SliceStable(names, func(i, j int) bool { return len(by[names[i]]) > len(by[names[j]]) })
	case projSortName:
		sort.SliceStable(names, func(i, j int) bool {
			return strings.ToLower(names[i]) < strings.ToLower(names[j])
		})
	}
	return names
}

const hotWindow = 7 * 24 * time.Hour

// hotFiles: the files written most in the project's sessions active this week.
func (m *Model) hotFiles(recs []*fav.Rec) []fav.FileCount {
	sum := map[string]int{}
	for _, r := range recs {
		if m.now.Sub(r.ActiveAt()) > hotWindow {
			continue
		}
		for p, n := range r.Files {
			sum[p] += n
		}
	}
	return fav.TopFiles(sum, 5)
}

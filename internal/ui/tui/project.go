package tui

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
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
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(m.groupLabel(name), inner)), "")

	dirs := topN(recs, func(r *tend.Rec) string { return r.Cwd }, 1)
	if p := m.proj.snap.Projects[groupProject(name)]; p != nil {
		body = append(body, m.projectLines(p, inner, labelW)...)
	} else if len(dirs) > 0 {
		if m.proj.snap.Ready() {
			add(render.GlyphProject+i18n.T("card.project"), i18n.F("project.unfiled_why", keyOf(inList, actEdit)))
		}
		dir := paths.Tilde(dirs[0].key)
		if i := slices.IndexFunc(recs, func(r *tend.Rec) bool { return r.Cwd == dirs[0].key }); recs[i].Host != "" {
			dir = recs[i].Host + ":" + dirs[0].key // another machine's directory: not checked here
		} else if !paths.IsDir(dirs[0].key) {
			dir += errSty.Render(i18n.T("project.missing"))
		}
		if extra := distinct(recs, func(r *tend.Rec) string { return r.Cwd }) - 1; extra > 0 {
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
	if bs := topN(recs, func(r *tend.Rec) string { return r.GitBranch }, 4); len(bs) > 0 {
		names := make([]string, len(bs))
		for i, b := range bs {
			names[i] = b.key
		}
		add(render.GlyphBranch+i18n.T("card.branch"), strings.Join(names, " · "))
	}

	favorites, claude, codex, turns, active, done, archived, live := 0, 0, 0, 0, 0, 0, 0, 0
	var first, last time.Time
	for _, r := range recs {
		if r.Favorite() {
			favorites++
		}
		switch r.Provider {
		case tend.ProviderClaude:
			claude++
		case tend.ProviderCodex:
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
		if _, ok := m.liveOf(r); ok {
			live++
		}
		if t := r.When(); !t.IsZero() && (first.IsZero() || t.Before(first)) {
			first = t
		}
		if t := m.when(r); t.After(last) {
			last = t
		}
	}
	add(render.GlyphSession+i18n.T("project.sessions_label"), i18n.F("project.sessions", len(recs), favorites))
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

// projectLines: the project's kind, owner and participants, and its directory on each machine (this machine's says
// whether it is there; another machine's cannot be checked from here).
func (m *Model) projectLines(p *task.Project, inner, labelW int) []string {
	s := &m.proj.snap
	lines := []string{dimmed.Render(render.Pad(render.GlyphProject+i18n.T("card.project"), labelW)) +
		render.Truncate(m.projectKindLine(p, false), inner-labelW)}
	type at struct{ machine, dir string }
	var dirs []at
	for _, r := range p.Repos {
		for mc, d := range r.Dirs {
			dirs = append(dirs, at{mc, d})
		}
	}
	slices.SortFunc(dirs, func(a, b at) int {
		if (a.machine == s.Here) != (b.machine == s.Here) {
			if a.machine == s.Here {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(a.machine, b.machine), strings.Compare(a.dir, b.dir))
	})
	machW := 0
	for _, d := range dirs {
		machW = max(machW, render.Width(m.machineLabel(d.machine)))
	}
	for i, d := range dirs {
		label := ""
		if i == 0 {
			label = render.GlyphDir + i18n.T("card.directory")
		}
		tail, tailW := "", 0
		if d.machine == s.Here {
			tail = i18n.T("project.here")
			if !paths.IsDir(d.dir) {
				tail = errSty.Render(i18n.T("project.not_here"))
			}
			tailW = render.Width(i18n.T("project.here"))
			if !paths.IsDir(d.dir) {
				tailW = render.Width(i18n.T("project.not_here"))
			}
		}
		room := inner - labelW
		head := render.Pad(m.machineLabel(d.machine), machW) + "  "
		path := render.Truncate(paths.Tilde(d.dir), max(4, room-render.Width(head)-tailW-1))
		gap := strings.Repeat(" ", max(1, room-render.Width(head)-render.Width(path)-tailW))
		lines = append(lines, dimmed.Render(render.Pad(label, labelW))+head+path+gap+tail)
	}
	return lines
}

func (m *Model) jumpTo(group string, r *tend.Rec) {
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
func topN(recs []*tend.Rec, key func(*tend.Rec) string, n int) []keyCount {
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

func distinct(recs []*tend.Rec, key func(*tend.Rec) string) int {
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

// orderGroups: the projects view orders groups by their newest record (the record order), by how many sessions they
// hold, or by the name shown (labels).
func orderGroups(keys []string, by map[string][]*tend.Rec, labels map[string]string, mode string) []string {
	switch mode {
	case projSortCount:
		sort.SliceStable(keys, func(i, j int) bool { return len(by[keys[i]]) > len(by[keys[j]]) })
	case projSortName:
		sort.SliceStable(keys, func(i, j int) bool {
			return strings.ToLower(labels[keys[i]]) < strings.ToLower(labels[keys[j]])
		})
	}
	return keys
}

const hotWindow = 7 * 24 * time.Hour

// hotFiles: the files written most in the project's sessions active this week.
func (m *Model) hotFiles(recs []*tend.Rec) []tend.FileCount {
	sum := map[string]int{}
	for _, r := range recs {
		if m.now.Sub(r.ActiveAt()) > hotWindow {
			continue
		}
		for p, n := range r.Files {
			sum[p] += n
		}
	}
	return tend.TopFiles(sum, 5)
}

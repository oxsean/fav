package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
)

// layout is how the Tasks view arranges its tasks; o (the sort key) moves to the next.
type layout int

const (
	layoutHome  layout = iota // what waits for you, then what runs; machines and 7 days of usage below
	layoutList                // every task: those that need you, open, finished
	layoutTree                // the same, subtasks under their parents
	layoutBoard               // a column per situation
)

var layoutKeys = []string{"tasks.layout_home", "tasks.layout_list", "tasks.layout_tree", "tasks.layout_board"}

// boardKinds are the board's columns, left to right; canceled tasks sit with the finished ones.
var boardKinds = []string{task.SitWaiting, task.SitRunning, task.SitQueued, task.SitBacklog, task.SitDone}

var sitKeys = map[string]string{task.SitWaiting: "tasks.sit_waiting", task.SitRunning: "tasks.sit_running", task.SitQueued: "tasks.sit_queued",
	task.SitBacklog: "tasks.sit_backlog", task.SitDone: "tasks.sit_done"}

func (m *Model) cycleLayout() {
	t := &m.tasks
	t.marked, t.anchor = nil, ""
	t.layout = (t.layout + 1) % layout(len(layoutKeys))
	m.filterTasks()
	m.flash(i18n.F("tasks.layout", i18n.T(layoutKeys[t.layout])))
}

// arrange puts the matching tasks in the order the current layout shows them. needs, open and closed are the list's
// three groups, each in its order.
func (m *Model) arrange(needs, open, closed []*task.Task) {
	t := &m.tasks
	t.depth, t.cols, t.split = nil, nil, 0
	switch t.layout {
	case layoutList:
		t.list = append(append(needs, open...), closed...)
	case layoutHome:
		var waits, runs []*task.Task
		for _, x := range append(append(needs, open...), closed...) {
			sit := t.st.Situation(x)
			switch {
			case sit.Kind == task.SitWaiting:
				waits = append(waits, x)
			case t.st.Running(x.ID):
				runs = append(runs, x)
			}
		}
		t.list, t.split = append(waits, runs...), len(waits)
	case layoutTree:
		all := append(append(needs, open...), closed...)
		shown := map[string]bool{}
		for _, x := range all {
			shown[x.ID] = true
		}
		t.depth = map[string]int{}
		t.list = nil
		var walk func(x *task.Task, d int)
		walk = func(x *task.Task, d int) {
			if _, done := t.depth[x.ID]; done {
				return
			}
			t.depth[x.ID] = d
			t.list = append(t.list, x)
			for _, k := range t.st.Children(x.ID) {
				if shown[k.ID] {
					walk(k, d+1)
				}
			}
		}
		for _, x := range all {
			if x.Parent == "" || !shown[x.Parent] {
				walk(x, 0)
			}
		}
	case layoutBoard:
		t.cols = make([][]*task.Task, len(boardKinds))
		for _, x := range append(append(needs, open...), closed...) {
			k := t.st.Situation(x).Kind
			if k == task.SitCanceled {
				k = task.SitDone
			}
			if i := slices.Index(boardKinds, k); i >= 0 {
				t.cols[i] = append(t.cols[i], x)
			}
		}
		t.list = nil
		for _, c := range t.cols {
			t.list = append(t.list, c...)
		}
	}
}

// boardAt is the column and row of list index i on the board.
func (m *Model) boardAt(i int) (col, row int) {
	for c, xs := range m.tasks.cols {
		if i < len(xs) {
			return c, i
		}
		i -= len(xs)
	}
	return 0, 0
}

// boardMove goes to the next non-empty column left (d < 0) or right, at the same row or its last.
func (m *Model) boardMove(d int) {
	t := &m.tasks
	col, row := m.boardAt(t.cursor)
	for c := col + d; c >= 0 && c < len(t.cols); c += d {
		if len(t.cols[c]) == 0 {
			continue
		}
		i := 0
		for _, xs := range t.cols[:c] {
			i += len(xs)
		}
		t.cursor = i + min(row, len(t.cols[c])-1)
		return
	}
}

// taskRow draws one task of the list or the tree, depth levels in.
func (m *Model) taskRow(x *task.Task, depth int, selected bool, w int) string {
	lead := " "
	if m.tasks.marked[x.ID] {
		lead = "*"
	}
	glyph, sty := taskGlyph(m, x)
	cell := ""
	if r := m.lastRun(x.ID); r != nil {
		cell = runStateText(r)
		if r.Machine != "" {
			cell += " · " + r.Machine
		}
	} else if sit := m.tasks.st.Situation(x); sit.Kind != task.SitWaiting || sit.Reason != task.WhyDispatch {
		cell = render.SitText(sit)
	}
	indent := strings.Repeat("  ", depth)
	titleW := max(4, w-4-len(indent)-render.Width(cell)-2)
	if selected {
		return selTitle.Render(render.Pad(lead+indent+glyph+" "+render.Pad(render.Truncate(x.Title, titleW), titleW)+"  "+cell, w))
	}
	if lead != " " {
		lead = accent.Render(lead)
	}
	return lead + indent + sty.Render(glyph) + " " + render.Pad(render.Truncate(x.Title, titleW), titleW) + "  " + dimmed.Render(cell)
}

// taskRows draws the list, the tree or the home from the list's scroll position.
func (m *Model) taskRows(out []string, y0, x0, w, h int) []string {
	t := &m.tasks
	head := func(key string, n int) string { return accent.Render(i18n.F(key, n)) }
	var bottom []string
	if t.layout == layoutHome && m.h > 24 {
		bottom = []string{"", fit(m.machinesLine(), w), fit(m.usageLine(), w)}
	}
	room := h - len(out) - len(bottom)
	rows := []int{} // list index per drawn line; -1 = a heading
	if t.layout == layoutHome {
		rows = append(rows, -1)
		if t.split == 0 {
			rows = append(rows, -2)
		}
		for i := range t.list {
			if i == t.split {
				rows = append(rows, -3)
			}
			rows = append(rows, i)
		}
		if t.split == len(t.list) {
			rows = append(rows, -3, -4)
		}
	} else {
		for i := range t.list {
			rows = append(rows, i)
		}
	}
	at := slices.Index(rows, t.cursor)
	if at < t.scroll {
		t.scroll = at
	}
	if at >= t.scroll+room {
		t.scroll = at - room + 1
	}
	t.scroll = max(0, min(t.scroll, len(rows)-1))
	for _, i := range rows[t.scroll:] {
		if len(out) >= h-len(bottom) {
			break
		}
		switch i {
		case -1:
			out = append(out, head("tasks.home_waits", t.split))
			continue
		case -2:
			out = append(out, dimmed.Render(" "+i18n.T("tasks.home_no_waits")))
			continue
		case -3:
			out = append(out, head("tasks.home_runs", len(t.list)-t.split))
			continue
		case -4:
			out = append(out, dimmed.Render(" "+i18n.T("tasks.home_no_runs")))
			continue
		}
		x, idx := t.list[i], i
		m.mark(y0+len(out), x0, w, func(mm *Model) {
			if mm.tasks.cursor == idx {
				mm.openTask()
			}
			mm.tasks.cursor = idx
		})
		out = append(out, m.taskRow(x, t.depth[x.ID], i == t.cursor, w))
	}
	for len(bottom) > 0 && len(out) < h-len(bottom) {
		out = append(out, "")
	}
	return append(out, bottom...)
}

// board draws a column per situation, each scrolled to keep the selected task in sight.
func (m *Model) board(out []string, y0, x0, w, h int) []string {
	t := &m.tasks
	n := len(boardKinds)
	colW := max(8, (w-(n-1)*2)/n)
	curCol, curRow := m.boardAt(t.cursor)
	room := h - len(out) - 1
	cols := make([][]string, n)
	for c, kind := range boardKinds {
		xs := t.cols[c]
		cols[c] = []string{accent.Render(render.Pad(i18n.F("tasks.board_col", i18n.T(sitKeys[kind]), len(xs)), colW))}
		first := 0
		if c == curCol && curRow >= room {
			first = curRow - room + 1
		}
		base := 0
		for _, ys := range t.cols[:c] {
			base += len(ys)
		}
		for r := first; r < len(xs) && len(cols[c]) <= room; r++ {
			x, idx := xs[r], base+r
			glyph, sty := taskGlyph(m, x)
			title := render.Truncate(x.Title, colW-3)
			line := " " + sty.Render(glyph) + " " + render.Pad(title, colW-3)
			if idx == t.cursor {
				line = selTitle.Render(render.Pad(" "+glyph+" "+title, colW))
			}
			m.mark(y0+len(out)+len(cols[c]), x0+c*(colW+2), colW, func(mm *Model) {
				if mm.tasks.cursor == idx {
					mm.openTask()
				}
				mm.tasks.cursor = idx
			})
			cols[c] = append(cols[c], line)
		}
		if len(xs) == 0 {
			cols[c] = append(cols[c], dimmed.Render(render.Pad(" -", colW)))
		}
	}
	for r := 0; r <= room; r++ {
		var parts []string
		for c := range cols {
			parts = append(parts, fit(at(cols[c], r), colW))
		}
		out = append(out, strings.Join(parts, "  "))
	}
	return out
}

var machineStateKeys = map[string]string{coord.MachineConnected: "tasks.machine_connected", coord.MachineOffline: "tasks.machine_offline",
	coord.MachineConnecting: "tasks.machine_connecting"}

// machinesLine is every machine with its state and load, on one line.
func (m *Model) machinesLine() string {
	var parts []string
	for _, mc := range m.tasks.machines {
		parts = append(parts, mc.Name+" "+i18n.F("tasks.machine_state", i18n.T(machineStateKeys[mc.State]), mc.Active, mc.Slots))
	}
	if len(parts) == 0 {
		return dimmed.Render(" " + i18n.T("tasks.no_machines"))
	}
	return dimmed.Render(" " + i18n.T("tasks.machines") + "  " + strings.Join(parts, "   "))
}

// usageLine is what the runs of the last 7 days spent.
func (m *Model) usageLine() string {
	var tokens int64
	var usd float64
	runs := 0
	since := time.Now().Add(-7 * 24 * time.Hour)
	for _, r := range m.tasks.st.Runs {
		at := r.QueuedAt
		if r.EndedAt != nil {
			at = *r.EndedAt
		}
		if r.Usage == nil || at.Before(since) {
			continue
		}
		runs++
		tokens += r.Usage.Input + r.Usage.CacheWrite + r.Usage.Output
		usd += r.Usage.CostUSD
	}
	s := i18n.F("tasks.usage7", runs, render.Tokens(tokens))
	if usd > 0 {
		s += " · $" + strconv.FormatFloat(usd, 'f', 2, 64)
	}
	return dimmed.Render(" " + s)
}

// extendMarks moves the cursor by d and marks every task from where the range started to it.
func (m *Model) extendMarks(d int) {
	t := &m.tasks
	x := m.selectedTask()
	if x == nil {
		return
	}
	if t.anchor == "" || !t.marked[t.anchor] {
		t.anchor = x.ID
	}
	t.cursor = min(max(t.cursor+d, 0), max(0, len(t.list)-1))
	from := slices.IndexFunc(t.list, func(y *task.Task) bool { return y.ID == t.anchor })
	lo, hi := min(from, t.cursor), max(from, t.cursor)
	t.marked = map[string]bool{}
	for _, y := range t.list[lo : hi+1] {
		t.marked[y.ID] = true
	}
	m.flash(i18n.F("tasks.marked", len(t.marked)))
}

// markedTasks are the marked tasks in the list's order.
func (m *Model) markedTasks() []*task.Task {
	var out []*task.Task
	for _, x := range m.tasks.list {
		if m.tasks.marked[x.ID] {
			out = append(out, x)
		}
	}
	return out
}

// markedToRun are the marked tasks a run dialog can start: to do and not running.
func (m *Model) markedToRun() []string {
	var out []string
	for _, x := range m.markedTasks() {
		if x.Status == task.StatusTodo && !m.tasks.st.Running(x.ID) {
			out = append(out, x.ID)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// toggleMarkedDone marks every marked task done, or reopens them all when all are finished already; u takes it back.
func (m *Model) toggleMarkedDone() tea.Cmd {
	xs := m.markedTasks()
	status := task.StatusTodo
	for _, x := range xs {
		if x.Status == task.StatusTodo {
			status = task.StatusDone
		}
	}
	was := map[string]string{}
	var cmds []tea.Cmd
	for _, x := range xs {
		if x.Status == status {
			continue
		}
		was[x.ID] = x.Status
		cmds = append(cmds, m.write(coord.MTaskStatus, task.TaskStatus{ID: x.ID, Status: status}, "", nil))
	}
	if len(cmds) == 0 {
		return nil
	}
	m.tasks.marked, m.tasks.anchor = nil, ""
	note := i18n.F("tasks.done_n", len(was))
	if status == task.StatusTodo {
		note = i18n.F("tasks.reopened_n", len(was))
	}
	cmds = append(cmds, func() tea.Msg {
		return taskDoneMsg{then: func(mm *Model) tea.Cmd {
			mm.offerUndo(note, func(mm *Model) tea.Cmd {
				var back []tea.Cmd
				for id, s := range was {
					back = append(back, mm.write(coord.MTaskStatus, task.TaskStatus{ID: id, Status: s}, "", nil))
				}
				back = append(back, func() tea.Msg { return taskDoneMsg{note: i18n.F("undo.done", note)} })
				return tea.Batch(back...)
			})
			return nil
		}}
	})
	return tea.Batch(cmds...)
}

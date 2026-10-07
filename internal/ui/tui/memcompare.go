package tui

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// The memory comparison: from the memory overlay, 「对比…」 picks another machine of the project and lists both
// machines' memories of its directories, paired as `tend memory diff` pairs them (projects.MemoryPairs) and compared by
// remote.CompareMemories: only on this side, only on the other, different, and what either holds under .incoming/ to
// merge; the same are counted. x ticks Claude memories and the copy buttons write them to either side through
// remote.CopyMemory, never over another; Enter reads one with a tab per side. Every call runs off the main loop.

// The comparison's groups, in the order listed.
const (
	cmpOnlyHere = iota
	cmpOnlyThere
	cmpDiffer
	cmpIncoming
)

type memCmp struct {
	back     *memOverlay
	label    string
	project  *task.Project
	from, to string // this side and the other ("" this machine)
	pairs    []remote.MemoryPair
	why      string
	loading  bool
	copying  bool
	seq      int
	cursor   int
	top      int
	ticks    map[string]bool
	done     string   // what the last copy wrote
	notes    []string // what it could not do as asked
	read     *memCmpRead
	readSeq  int
}

// cmpRow is one row of the comparison: an entry compared, or one side's memory under .incoming/.
type cmpRow struct {
	group int
	pair  int
	e     memory.Entry
	side  int         // cmpIncoming: 0 this side, 1 the other
	it    memory.Item // cmpIncoming
}

func (r cmpRow) name() string {
	if r.group == cmpIncoming {
		return r.it.Title
	}
	return r.e.Name
}

func (r cmpRow) key() string {
	return strings.Join([]string{strconv.Itoa(r.group), strconv.Itoa(r.pair), strconv.Itoa(r.side), r.e.Kind, r.name(), r.it.File}, "\x00")
}

// item is r's memory on side (0 this side, 1 the other), nil when that side has none.
func (r cmpRow) item(side int) *memory.Item {
	switch {
	case r.group == cmpIncoming && r.side == side:
		return &r.it
	case r.group == cmpIncoming:
		return nil
	case side == 0:
		return r.e.Here
	}
	return r.e.There
}

func (r cmpRow) copyable() bool { return r.group != cmpIncoming && r.e.Kind == memory.KindClaude }

// to says whether r can be copied to the other side (toThere) or this one.
func (r cmpRow) to(toThere bool) bool {
	return r.copyable() && (r.group == cmpDiffer || toThere && r.group == cmpOnlyHere || !toThere && r.group == cmpOnlyThere)
}

func (c *memCmp) rows() []cmpRow {
	var out []cmpRow
	for g := cmpOnlyHere; g <= cmpIncoming; g++ {
		for i, p := range c.pairs {
			var es []memory.Entry
			switch g {
			case cmpOnlyHere:
				es = p.Diff.OnlyHere
			case cmpOnlyThere:
				es = p.Diff.OnlyThere
			case cmpDiffer:
				es = p.Diff.Differ
			case cmpIncoming:
				for side, sets := range [][]memory.Set{p.From, p.To} {
					for _, s := range sets {
						for _, it := range s.Incoming {
							out = append(out, cmpRow{group: g, pair: i, side: side, it: it})
						}
					}
				}
			}
			for _, e := range es {
				out = append(out, cmpRow{group: g, pair: i, e: e})
			}
		}
	}
	return out
}

func (c *memCmp) clamp() {
	c.cursor = min(max(c.cursor, 0), max(0, len(c.rows())-1))
}

func (c *memCmp) row() (cmpRow, bool) {
	rows := c.rows()
	if len(rows) == 0 {
		return cmpRow{}, false
	}
	return rows[min(max(c.cursor, 0), len(rows)-1)], true
}

// picked are the ticked rows, else the one under the cursor.
func (c *memCmp) picked() []cmpRow {
	var out []cmpRow
	for _, r := range c.rows() {
		if c.ticks[r.key()] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		if r, ok := c.row(); ok {
			out = append(out, r)
		}
	}
	return out
}

// memProject is the project a memory view's key names, nil for a group or directory of no project.
func (m *Model) memProject(key string) *task.Project {
	id, ok := strings.CutPrefix(key, projKey)
	if !ok {
		return nil
	}
	return m.proj.snap.Projects[id]
}

// askCompare picks the machine to compare the overlay's memories with: the project's other machines, those that cannot
// be compared with now marked with why.
func (m *Model) askCompare() {
	o := m.ov.mem
	v := o.view
	p := m.memProject(o.key)
	switch {
	case p == nil:
		m.flash(i18n.F("memory.cmp_no_project", v.label))
		return
	case len(v.machines) < 2:
		m.flash(i18n.F("memory.cmp_no_other", v.label))
		return
	}
	back := func(mm *Model) { mm.ov = overlay{kind: ovMemory, focus: -1, mem: o} }
	from := v.machines[0].machine
	var items []item
	for _, ms := range v.machines[1:] {
		label := m.memMachineLabel(ms.machine)
		if why := m.memoryWhy(ms.machine, remote.MMemoryList); why != "" {
			label += "  ·  " + why
		}
		items = append(items, item{name: ms.machine, label: label})
	}
	m.openPicker(i18n.F("memory.cmp_pick", v.label), "", items, false, nil, func(mm *Model, chosen []string) {
		back(mm)
		if len(chosen) == 0 {
			return
		}
		if why := mm.memoryWhy(chosen[0], remote.MMemoryList); why != "" {
			mm.flash(why)
			return
		}
		c := &memCmp{back: o, label: v.label, project: p, from: from, to: chosen[0]}
		mm.ov = overlay{kind: ovMemCompare, focus: -1, mcmp: c}
		mm.pending = tea.Batch(mm.pending, mm.compareMemories(c))
	})
	m.ov.back = back
}

type memCmpMsg struct {
	c     *memCmp
	seq   int
	pairs []remote.MemoryPair
	why   string
	old   string // the machine whose tend lacks memory.ls
}

// compareMemories pairs the project's directories on both machines and compares their memories.
func (m *Model) compareMemories(c *memCmp) tea.Cmd {
	c.seq++
	c.loading, c.why = true, ""
	for _, name := range []string{c.from, c.to} {
		if why := m.memoryWhy(name, remote.MMemoryList); why != "" {
			c.loading, c.why = false, why
			return nil
		}
	}
	peer, seq, here := m.peerFn(), c.seq, m.proj.snap.Here
	labels := map[string]string{c.from: m.memMachineLabel(c.from), c.to: m.memMachineLabel(c.to)}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := memCmpMsg{c: c, seq: seq}
		from, to, err := reachTwo(ctx, peer, c.from, c.to)
		if err != nil {
			msg.why = err.Error()
			return msg
		}
		pairs := projects.MemoryPairs(c.project, "", cmp.Or(c.from, here), cmp.Or(c.to, here), from.End(), to.End())
		if len(pairs) == 0 {
			msg.why = i18n.F("cli.memory.no_pair", c.project.Name, labels[c.from], labels[c.to])
			return msg
		}
		res, err := remote.CompareMemories(ctx, from, to, pairs)
		if err != nil {
			name := peerOf(err, c.from, c.to)
			msg.why = refusalOf(err, to, labels[name])
			if wire.Code(err) == wire.CodeUnknownMethod {
				msg.old = name
			}
			return msg
		}
		msg.pairs = res
		return msg
	}
}

func reachTwo(ctx context.Context, peer func(context.Context, string) (remote.Peer, error), a, b string) (remote.Peer, remote.Peer, error) {
	pa, err := peer(ctx, a)
	if err != nil {
		return remote.Peer{}, remote.Peer{}, err
	}
	pb, err := peer(ctx, b)
	return pa, pb, err
}

// peerOf is which of machines from and to the error of a call between them came from.
func peerOf(err error, from, to string) string {
	var pe *remote.PeerError
	if errors.As(err, &pe) && pe.Peer.Name == from {
		return from
	}
	return to
}

// refusalOf says why the machine err came from (to unless the error names another), called label, did not answer.
func refusalOf(err error, to remote.Peer, label string) string {
	p := to
	var pe *remote.PeerError
	if errors.As(err, &pe) {
		p = pe.Peer
	}
	return remote.MemoryRefusal(p, label, err)
}

func (msg memCmpMsg) apply(m *Model) tea.Cmd {
	if msg.old != "" {
		m.lackOn(msg.old, remote.MMemoryList)
	}
	c := msg.c
	if msg.seq != c.seq {
		return nil
	}
	c.loading, c.why, c.pairs = false, msg.why, msg.pairs
	keys := map[string]bool{}
	for _, r := range c.rows() {
		keys[r.key()] = true
	}
	for k := range c.ticks {
		if !keys[k] {
			delete(c.ticks, k)
		}
	}
	c.clamp()
	return nil
}

func (m *Model) memCompareKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.ov.mcmp
	if c.read != nil {
		m.memCmpReadKey(msg)
		return nil
	}
	n, page := len(c.rows()), max(1, m.ov.room)
	switch a := keyAct(inMemCompare, msg.String()); a {
	case actDown, actUp, actPageDown, actPageUp, actHalfDown, actHalfUp, actTop, actBottom:
		c.cursor = map[act]int{actDown: c.cursor + 1, actUp: c.cursor - 1, actPageDown: c.cursor + page, actPageUp: c.cursor - page,
			actHalfDown: c.cursor + max(1, page/2), actHalfUp: c.cursor - max(1, page/2), actTop: 0, actBottom: n - 1}[a]
		c.clamp()
		m.ov.focus = -1
	case actTick:
		m.tickCompared()
	case actClose:
		m.ov = overlay{kind: ovMemory, focus: -1, mem: c.back}
	default:
		m.dialogKey(a, func() { m.readCompared(c.cursor) })
	}
	return nil
}

// tickCompared ticks the Claude memory under the cursor, or unticks it.
func (m *Model) tickCompared() {
	c := m.ov.mcmp
	r, ok := c.row()
	switch {
	case !ok:
	case r.group == cmpIncoming:
		m.flash(i18n.T("memory.cmp_merge_by_hand"))
	case !r.copyable():
		m.flash(i18n.T("memory.codex_compare_only"))
	default:
		if c.ticks == nil {
			c.ticks = map[string]bool{}
		}
		if c.ticks[r.key()] {
			delete(c.ticks, r.key())
		} else {
			c.ticks[r.key()] = true
		}
	}
}

type cmpJob struct {
	mp remote.MemoryPair
	e  memory.Entry
}

type memCopiedMsg struct {
	c        *memCmp
	to       string
	copied   int
	incoming int
	stale    []string
	over     bool
	why      string
	old      string // the machine whose tend lacks a memory method
}

// copyMemories copies the ticked Claude memories, else the one under the cursor, to the other side (toThere) or to
// this one, then compares again.
func (m *Model) copyMemories(toThere bool) {
	c := m.ov.mcmp
	if c.loading || c.copying || c.why != "" {
		return
	}
	src, dst := c.to, c.from
	if toThere {
		src, dst = c.from, c.to
	}
	var jobs []cmpJob
	codex := true
	for _, r := range c.picked() {
		codex = codex && r.group != cmpIncoming && !r.copyable()
		if !r.to(toThere) {
			continue
		}
		j := cmpJob{c.pairs[r.pair], r.e}
		if !toThere {
			j = cmpJob{j.mp.Swap(), j.e.Swap()}
		}
		jobs = append(jobs, j)
	}
	switch {
	case len(jobs) == 0 && codex:
		m.flash(i18n.T("memory.codex_compare_only"))
		return
	case len(jobs) == 0:
		m.flash(i18n.F("memory.cmp_nothing", m.memMachineLabel(dst)))
		return
	}
	if why := m.memoryWhy(dst, remote.MMemoryPut); why != "" {
		m.flash(why)
		return
	}
	c.copying, c.done, c.notes = true, "", nil
	peer, srcL, dstL := m.peerFn(), m.memMachineLabel(src), m.memMachineLabel(dst)
	m.pending = tea.Batch(m.pending, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := memCopiedMsg{c: c, to: dst}
		from, to, err := reachTwo(ctx, peer, src, dst)
		if err != nil {
			msg.why = err.Error()
			return msg
		}
		for _, j := range jobs {
			res, err := remote.CopyMemory(ctx, from, to, j.mp, j.e)
			switch {
			case wire.Code(err) == wire.CodeStale:
				msg.stale = append(msg.stale, j.e.Name)
				continue
			case err != nil:
				name := peerOf(err, src, dst)
				msg.why = refusalOf(err, to, map[string]string{src: srcL, dst: dstL}[name])
				if wire.Code(err) == wire.CodeUnknownMethod {
					msg.old = name
				}
				return msg
			case res.Incoming:
				msg.incoming++
			default:
				msg.copied++
			}
			msg.over = msg.over || res.Over
		}
		return msg
	})
}

// apply says what the copy did, below the list and in the status line, and reads both machines again.
func (msg memCopiedMsg) apply(m *Model) tea.Cmd {
	if msg.old != "" {
		m.lackOn(msg.old, remote.MMemoryPut)
	}
	c, to := msg.c, m.memMachineLabel(msg.to)
	c.copying, c.ticks = false, nil
	var notes []string
	if msg.copied > 0 {
		c.done = i18n.F("memory.cmp_copied", to, msg.copied)
	}
	if msg.incoming > 0 {
		notes = append(notes, i18n.F("memory.cmp_incoming_note", msg.incoming, to))
	}
	for _, name := range msg.stale {
		notes = append(notes, i18n.F("memory.cmp_stale", name, to))
	}
	if msg.over {
		notes = append(notes, i18n.F("memory.cmp_over", to))
	}
	if msg.why != "" {
		notes = append(notes, msg.why)
	}
	c.notes = notes
	switch {
	case c.done != "":
		m.flash(c.done)
	case len(notes) > 0:
		m.flash(notes[0])
	}
	return tea.Batch(m.compareMemories(c), m.rereadMemories(c.back.key))
}

// memCmpRead is a compared memory open in the reader, a tab per side.
type memCmpRead struct {
	row     cmpRow
	tab     int
	texts   [2]string
	ats     [2]time.Time
	whys    [2]string
	loading [2]bool
	seq     int
}

type memCmpTextMsg struct {
	c    *memCmp
	seq  int
	side int
	text string
	at   time.Time
	why  string
}

// readCompared opens row i in the reader, each side's text read through memory.read on its machine.
func (m *Model) readCompared(i int) {
	c := m.ov.mcmp
	rows := c.rows()
	if i < 0 || i >= len(rows) {
		return
	}
	c.cursor, c.readSeq = i, c.readSeq+1
	r := &memCmpRead{row: rows[i], seq: c.readSeq}
	if r.row.item(0) == nil {
		r.tab = 1
	}
	c.read, m.ov.cursor = r, 0
	peer := m.peerFn()
	for side, machine := range []string{c.from, c.to} {
		it := r.row.item(side)
		if it == nil {
			continue
		}
		r.loading[side] = true
		seq, item, label := r.seq, *it, m.memMachineLabel(machine)
		m.pending = tea.Batch(m.pending, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
			defer cancel()
			msg := memCmpTextMsg{c: c, seq: seq, side: side}
			p, err := peer(ctx, machine)
			if err != nil {
				msg.why = err.Error()
				return msg
			}
			t, err := remote.ReadMemory(ctx, p, item)
			if err != nil {
				msg.why = remote.MemoryRefusal(p, label, err)
				return msg
			}
			msg.text, msg.at = t.Text, t.At
			return msg
		})
	}
}

func (msg memCmpTextMsg) apply(m *Model) tea.Cmd {
	r := msg.c.read
	if r == nil || r.seq != msg.seq {
		return nil
	}
	r.loading[msg.side], r.texts[msg.side], r.ats[msg.side], r.whys[msg.side] = false, msg.text, msg.at, msg.why
	return nil
}

func (m *Model) memCmpReadKey(msg tea.KeyPressMsg) {
	r := m.ov.mcmp.read
	switch a := keyAct(inReader, msg.String()); a {
	case actClose:
		m.ov.mcmp.read, m.ov.cursor = nil, 0
	case actTabNext, actTabPrev:
		if r.row.item(1-r.tab) != nil {
			r.tab, m.ov.cursor = 1-r.tab, 0
		}
	case actCopy:
		if err := copyText(r.texts[r.tab]); err != nil {
			m.flash(i18n.F("flash.clipboard_unavailable", err))
		} else {
			m.flash(i18n.F("memory.copied", len([]rune(r.texts[r.tab]))))
		}
	default:
		m.scrollKey(a)
	}
}

func (m *Model) memCompareButtons() []btn {
	c := m.ov.mcmp
	read := btn{keyed(keyName("enter"), i18n.T("memory.btn_read")), true, nil}
	tick := btn{keyed(keyOf(inMemCompare, actTick), i18n.T("memory.btn_tick")), false, nil}
	there := btn{i18n.F("memory.btn_copy_to", m.memMachineLabel(c.to)), false, nil}
	here := btn{i18n.T("memory.btn_copy_here"), false, nil}
	if c.from != "" {
		here.label = i18n.F("memory.btn_copy_to", m.memMachineLabel(c.from))
	}
	if len(c.rows()) > 0 {
		read.act = func(mm *Model) { mm.readCompared(mm.ov.mcmp.cursor) }
		tick.act = (*Model).tickCompared
		if !c.loading && !c.copying && c.why == "" {
			there.act = func(mm *Model) { mm.copyMemories(true) }
			here.act = func(mm *Model) { mm.copyMemories(false) }
		}
	}
	back := btn{keyed(keyName("esc"), i18n.T("memory.btn_back")), false, func(mm *Model) {
		mm.ov = overlay{kind: ovMemory, focus: -1, mem: mm.ov.mcmp.back}
	}}
	return []btn{read, tick, there, here, back}
}

// cmpLine is one line of the comparison's list; row is the row it shows, -1 for a heading.
type cmpLine struct {
	text string
	row  int
}

func (m *Model) memCompareLines(c *memCmp, inner int) []cmpLine {
	switch {
	case c.why != "":
		var out []cmpLine
		for _, l := range render.Wrap(c.why, inner) {
			out = append(out, cmpLine{warnSty.Render(l), -1})
		}
		return out
	case c.loading && c.pairs == nil:
		return []cmpLine{{dimmed.Render(i18n.T("memory.loading")), -1}}
	}
	rows := c.rows()
	if len(rows) == 0 {
		return []cmpLine{{dimmed.Render(i18n.T("memory.cmp_alike")), -1}}
	}
	n := map[int]int{}
	for _, r := range rows {
		n[r.group]++
	}
	fromL, toL := m.memMachineLabel(c.from), m.memMachineLabel(c.to)
	var out []cmpLine
	for i, r := range rows {
		if i == 0 || rows[i-1].group != r.group {
			if i > 0 {
				out = append(out, cmpLine{"", -1})
			}
			head := map[int]string{cmpOnlyHere: i18n.F("memory.cmp_only", fromL, n[cmpOnlyHere]), cmpOnlyThere: i18n.F("memory.cmp_only", toL, n[cmpOnlyThere]),
				cmpDiffer: i18n.F("memory.cmp_differ", n[cmpDiffer]), cmpIncoming: i18n.F("memory.cmp_incoming", n[cmpIncoming])}[r.group]
			out = append(out, cmpLine{accent.Render(render.Truncate(head, inner)), -1})
		}
		out = append(out, cmpLine{m.cmpRowLine(c, r, i == c.cursor, inner), i})
	}
	return out
}

// cmpLeadW is the width of a row's cursor column, its box after it.
func cmpLeadW() int { return render.Width(render.GlyphArrow + " ") }

// cmpRowLine: the box, then title — description · age; both ages for one that differs; where one to merge lies.
func (m *Model) cmpRowLine(c *memCmp, r cmpRow, sel bool, inner int) string {
	box := "    "
	if r.copyable() {
		box = "[ ] "
		if c.ticks[r.key()] {
			box = "[+] "
		}
	}
	var text, note string
	switch it := cmp.Or(r.e.Here, r.e.There); {
	case r.group == cmpIncoming:
		text = i18n.F("memory.cmp_incoming_item", m.memMachineLabel([]string{c.from, c.to}[r.side]), r.it.Title, filepath.Base(r.it.File), render.When(r.it.At, m.now))
	case r.group == cmpDiffer:
		text = i18n.F("memory.cmp_both", it.Title, m.memMachineLabel(c.from), render.When(r.e.Here.At, m.now), m.memMachineLabel(c.to), render.When(r.e.There.At, m.now))
	case it.Description != "":
		text = i18n.F("memory.item", it.Title, it.Description, render.When(it.At, m.now))
	default:
		text = i18n.F("memory.item_bare", it.Title, render.When(it.At, m.now))
	}
	if r.group != cmpIncoming && r.e.Kind != memory.KindClaude {
		note = "  " + i18n.T("memory.cmp_codex")
	}
	lead := render.GlyphArrow + " "
	room := inner - 2 - cmpLeadW() - len(box) - render.Width(note)
	text = render.Truncate(text, room)
	text += strings.Repeat(" ", max(0, room-render.Width(text)))
	if sel {
		return "  " + selTitle.Render(lead+box+text) + dimmed.Background(cSelBg).Render(note)
	}
	return "  " + strings.Repeat(" ", cmpLeadW()) + box + text + dimmed.Render(note)
}

func (m *Model) renderMemCompare() string {
	c := m.ov.mcmp
	if c.read != nil {
		return m.renderMemCmpRead()
	}
	w := m.ovWidth()
	inner := w - 4
	fromL, toL := m.memMachineLabel(c.from), m.memMachineLabel(c.to)
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("memory.cmp_title", c.label, fromL, toL), inner))}
	for _, p := range c.pairs {
		body = append(body, dimmed.Render(render.Truncate(fromL+" "+m.memPath(c.from, p.Dirs.From)+"  ·  "+toL+" "+m.memPath(c.to, p.Dirs.To), inner)))
	}
	body = append(body, "")
	var tail []string
	if c.pairs != nil && c.why == "" {
		same, loose := 0, 0
		for _, p := range c.pairs {
			same += len(p.Diff.Same)
			for _, e := range p.Diff.Same {
				if e.Loose {
					loose++
				}
			}
		}
		line := i18n.F("memory.cmp_same_bare", same)
		if loose > 0 {
			line = i18n.F("memory.cmp_same", same, loose)
		}
		tail = append(tail, "", dimmed.Render(render.Truncate(line, inner)))
	}
	if c.copying {
		tail = append(tail, dimmed.Render(i18n.T("memory.cmp_copying")))
	}
	if c.done != "" {
		tail = append(tail, render.Truncate(c.done, inner))
	}
	for _, n := range c.notes {
		for _, l := range render.Wrap(n, inner) {
			tail = append(tail, warnSty.Render(l))
		}
	}
	lines := m.memCompareLines(c, inner)
	room := max(3, m.h-4-len(body)-len(tail)-6)
	sel := -1
	for i, l := range lines {
		if l.row == c.cursor {
			sel = i
		}
	}
	if sel >= 0 {
		c.top = min(max(c.top, sel-room+1), sel)
	}
	c.top = min(max(c.top, 0), max(0, len(lines)-room))
	m.ov.room = room
	boxX := ovPad + 2 + cmpLeadW()
	for i := c.top; i < min(len(lines), c.top+room); i++ {
		if idx := lines[i].row; idx >= 0 {
			y := len(body) + 1
			pick := func(mm *Model) {
				if mm.ov.mcmp.cursor == idx {
					mm.readCompared(idx)
					return
				}
				mm.ov.mcmp.cursor, mm.ov.focus = idx, -1
			}
			m.mark(y, ovPad, boxX-ovPad, pick)
			m.mark(y, boxX, 4, func(mm *Model) {
				mm.ov.mcmp.cursor, mm.ov.focus = idx, -1
				mm.tickCompared()
			})
			m.mark(y, boxX+4, inner-(boxX+4-ovPad), pick)
		}
		body = append(body, lines[i].text)
	}
	if len(lines) > room {
		body = append(body, dimmed.Render(i18n.F("overlay.line_range", c.top+1, min(len(lines), c.top+room), len(lines))))
	}
	body = append(body, tail...)
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, m.memCompareButtons())...)
	return ovRender(body, w)
}

func (m *Model) renderMemCmpRead() string {
	c := m.ov.mcmp
	r := c.read
	w := max(44, m.w-8)
	inner := w - 4
	machines := [2]string{c.from, c.to}
	it := r.row.item(r.tab)
	title := r.row.name()
	if it != nil {
		title = it.Title
	}
	var tabs []string
	for side, machine := range machines {
		label := " " + m.memMachineLabel(machine) + " "
		switch {
		case side == r.tab:
			tabs = append(tabs, selTitle.Render(label))
		case r.row.item(side) == nil:
			tabs = append(tabs, dimmed.Render(i18n.F("memory.cmp_absent", m.memMachineLabel(machine))))
		default:
			tabs = append(tabs, dimmed.Render(label))
		}
	}
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(title, inner)), fit(strings.Join(tabs, "  "), inner)}
	if it != nil {
		kind, file := i18n.T("memory.kind_claude"), filepath.Base(it.File)
		switch {
		case r.row.group == cmpIncoming:
			file = ".incoming/" + file
		case r.row.e.Kind != memory.KindClaude:
			kind, file = i18n.T("memory.kind_codex"), file+":"+strconv.Itoa(it.Line)
		}
		at := cmp.Or(r.ats[r.tab], it.At)
		body = append(body, dimmed.Render(render.Truncate(i18n.F("memory.read_facts", m.memMachineLabel(machines[r.tab]), kind, file, render.When(at, m.now)), inner)))
	}
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	room := max(1, m.h-4-len(body)-4)
	var lines []string
	switch {
	case r.loading[r.tab]:
		lines = []string{dimmed.Render(i18n.T("memory.loading"))}
	case r.whys[r.tab] != "":
		lines = render.Wrap(r.whys[r.tab], inner-2)
	default:
		lines = render.Wrap(strings.TrimRight(r.texts[r.tab], "\n"), inner-2)
	}
	end := m.scrollWindow(len(lines), room)
	bar := scrollbar(len(lines), m.ov.cursor, room)
	head := len(body)
	for i := m.ov.cursor; i < end; i++ {
		body = append(body, fit(lines[i], inner-2)+" "+bar[i-m.ov.cursor])
	}
	for len(body) < room+head {
		body = append(body, "")
	}
	hint := i18n.F("memory.cmp_read_hint", keyName("tab"), keyOf(inReader, actCopy), keyName("esc"))
	if len(lines) > room {
		hint = i18n.F("msg.hint_scroll", hint)
	}
	body = append(body, "", dimmed.Render(hint))
	return ovRender(body, w)
}

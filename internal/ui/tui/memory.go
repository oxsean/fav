package tui

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// The memory overlay: the agents' memories of a project on each machine it has a directory on, or of a session's
// directory on its machine, listed through memory.ls there (this machine in process), off the main loop, as `tend
// memory` lists them. Enter reads one through memory.read with the reader's keys; D puts a Claude memory into its
// machine's trash (memory.trash) and u takes it back (memory.restore). The project block's memory line counts what the
// same reads found, read again after memEvery.

const memEvery = 30 * time.Second

// memTarget is what the overlay reads: the directories on each machine, this one ("") first.
type memTarget struct {
	key   string
	label string
	on    []memOn
}

type memOn struct {
	machine string // "" this machine
	dirs    []string
}

func (t *memTarget) add(machine, dir string) {
	if dir == "" {
		return
	}
	i := slices.IndexFunc(t.on, func(o memOn) bool { return o.machine == machine })
	if i < 0 {
		t.on, i = append(t.on, memOn{machine: machine}), len(t.on)
	}
	if !slices.Contains(t.on[i].dirs, dir) {
		t.on[i].dirs = append(t.on[i].dirs, dir)
	}
}

func (t *memTarget) sort() {
	slices.SortStableFunc(t.on, func(a, b memOn) int {
		if (a.machine == "") != (b.machine == "") {
			if a.machine == "" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.machine, b.machine)
	})
}

// memSets is what one machine answered for its directories.
type memSets struct {
	memOn
	sets    []memory.Set
	why     string // why it was not read
	loading bool
}

// memView is one target's reads, kept for the project block and the overlay.
type memView struct {
	key      string
	label    string
	machines []*memSets
	at       time.Time // when they were last asked
	seq      int
}

func (v *memView) busy() bool {
	return slices.ContainsFunc(v.machines, func(ms *memSets) bool { return ms.loading })
}

func (v *memView) same(t memTarget) bool {
	return slices.EqualFunc(v.machines, t.on, func(ms *memSets, o memOn) bool {
		return ms.machine == o.machine && slices.Equal(ms.dirs, o.dirs)
	})
}

func (v *memView) machine(name string) *memSets {
	for _, ms := range v.machines {
		if ms.machine == name {
			return ms
		}
	}
	return nil
}

// memEntry is one memory of a view, with the machine it is on and its set's kind.
type memEntry struct {
	memory.Item
	Machine string
	Kind    string
}

// entries are v's memories in the order the overlay lists them.
func (v *memView) entries() []memEntry {
	var out []memEntry
	for _, ms := range v.machines {
		for _, s := range ms.sets {
			for _, it := range s.Items {
				out = append(out, memEntry{Item: it, Machine: ms.machine, Kind: s.Kind})
			}
		}
	}
	return out
}

// memOverlay is the overlay's state: the view, the memory under the cursor, the one open in the reader.
type memOverlay struct {
	key    string
	view   *memView
	cursor int
	top    int // the first list row shown
	read   *memRead
	seq    int
}

type memRead struct {
	item    memEntry
	text    string
	at      time.Time
	why     string
	loading bool
	seq     int
}

func (o *memOverlay) item() memEntry {
	es := o.view.entries()
	if len(es) == 0 {
		return memEntry{}
	}
	return es[min(max(o.cursor, 0), len(es)-1)]
}

func (o *memOverlay) clamp() {
	o.cursor = min(max(o.cursor, 0), max(0, len(o.view.entries())-1))
}

// memTargetHere is what i reads under the cursor: a session's project, else its directory on its machine; a group
// heading's project, else its sessions' directories.
func (m *Model) memTargetHere() (memTarget, bool) {
	if r := m.current(); r != nil {
		if p := m.proj.snap.Of(r); p != nil {
			return m.projectMemTarget(p), true
		}
		d := projects.Dir(r)
		t := memTarget{key: "\x01" + r.Host + "\x00" + d, label: cmp.Or(r.Group(), d)}
		t.add(r.Host, d)
		return t, true
	}
	if g := m.groupUnderCursor(); m.view == viewProjects && g != "" {
		return m.groupMemTarget(g), true
	}
	return memTarget{}, false
}

// groupMemTarget: group g's project, else the directories of its sessions; its key is g.
func (m *Model) groupMemTarget(g string) memTarget {
	if p := m.proj.snap.Projects[groupProject(g)]; p != nil {
		return m.projectMemTarget(p)
	}
	t := memTarget{key: g, label: m.groupLabel(g)}
	for _, r := range m.groups[g] {
		t.add(r.Host, projects.Dir(r))
	}
	t.sort()
	return t
}

// projectMemTarget: p's directories on each machine; its key is its group's.
func (m *Model) projectMemTarget(p *task.Project) memTarget {
	t := memTarget{key: projKey + p.ID, label: p.Name}
	var machines []string
	for _, r := range p.Repos {
		for mc := range r.Dirs {
			if !slices.Contains(machines, mc) {
				machines = append(machines, mc)
			}
		}
	}
	for _, mc := range machines {
		name := mc
		if mc == m.proj.snap.Here {
			name = ""
		}
		for _, d := range projects.DirsOn(p, mc) {
			t.add(name, d)
		}
	}
	t.sort()
	return t
}

// memViewOf is t's view, a fresh one when its machines or directories changed.
func (m *Model) memViewOf(t memTarget) *memView {
	if m.mems == nil {
		m.mems = map[string]*memView{}
	}
	v := m.mems[t.key]
	if v != nil && v.same(t) {
		v.label = t.label
		return v
	}
	nv := &memView{key: t.key, label: t.label}
	if v != nil {
		nv.seq = v.seq
	}
	for _, on := range t.on {
		nv.machines = append(nv.machines, &memSets{memOn: on})
	}
	m.mems[t.key] = nv
	return nv
}

func (m *Model) memMachineLabel(name string) string {
	if name == "" {
		return i18n.T("project.this_machine")
	}
	return name
}

// memoryWhy is why machine name's memories cannot be reached through method now, "" when they can.
func (m *Model) memoryWhy(name, method string) string {
	return m.ownWhy(name, method, "memory.not_mine", "cli.memory.server_old")
}

func (m *Model) lackOn(name, method string) {
	if hr := m.remote[name]; hr != nil {
		hr.lack(method)
	}
}

// memoryHere reads the memories of the group heading under the cursor for the project block, unless they are fresh.
func (m *Model) memoryHere() tea.Cmd {
	if m.ov.active() || !m.onGroupHeader() {
		return nil
	}
	v := m.memViewOf(m.groupMemTarget(m.groupUnderCursor()))
	if v.busy() || time.Since(v.at) < memEvery {
		return nil
	}
	return m.readMemories(v)
}

type memListMsg struct {
	key, machine string
	seq          int
	sets         []memory.Set
	why          string
	old          bool
}

// readMemories asks every machine of v for its memories, each on its own off the main loop.
func (m *Model) readMemories(v *memView) tea.Cmd {
	v.at = time.Now()
	v.seq++
	var cmds []tea.Cmd
	for _, ms := range v.machines {
		if why := m.memoryWhy(ms.machine, remote.MMemoryList); why != "" {
			ms.why, ms.sets, ms.loading = why, nil, false
			continue
		}
		ms.loading = true
		peer, key, seq, on := m.peerFn(), v.key, v.seq, ms.memOn
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
			defer cancel()
			msg := memListMsg{key: key, machine: on.machine, seq: seq}
			p, err := peer(ctx, on.machine)
			if err != nil {
				msg.why = err.Error()
				return msg
			}
			var l remote.MemoryList
			if err := p.Call(ctx, remote.MMemoryList, remote.MemoryListParams{Dirs: on.dirs, Global: true}, &l); err != nil {
				msg.why, msg.old = remote.MemoryRefusal(p, hostLabel(on.machine), err), wire.Code(err) == wire.CodeUnknownMethod
				return msg
			}
			msg.sets = l.Sets
			return msg
		})
	}
	return tea.Batch(cmds...)
}

func (msg memListMsg) apply(m *Model) tea.Cmd {
	if msg.old {
		m.lackOn(msg.machine, remote.MMemoryList)
	}
	v := m.mems[msg.key]
	if v == nil || msg.seq != v.seq {
		return nil
	}
	ms := v.machine(msg.machine)
	if ms == nil {
		return nil
	}
	ms.loading, ms.why, ms.sets = false, msg.why, msg.sets
	if m.ov.kind == ovMemory && m.ov.mem.view == v {
		m.ov.mem.clamp()
	}
	return nil
}

// openMemory opens the overlay on what is under the cursor and reads it.
func (m *Model) openMemory() tea.Cmd {
	t, ok := m.memTargetHere()
	if !ok {
		m.flash(i18n.T("memory.nothing"))
		return nil
	}
	v := m.memViewOf(t)
	m.closeOverlay()
	m.ov = overlay{kind: ovMemory, focus: -1, mem: &memOverlay{key: t.key, view: v}}
	return m.readMemories(v)
}

func (m *Model) memoryKey(msg tea.KeyPressMsg) tea.Cmd {
	o := m.ov.mem
	if o.read != nil {
		switch a := keyAct(inReader, msg.String()); a {
		case actClose:
			o.read, m.ov.cursor = nil, 0
		case actTabNext:
			m.readMemory(o.cursor + 1)
		case actTabPrev:
			m.readMemory(o.cursor - 1)
		case actCopy:
			if err := copyText(o.read.text); err != nil {
				m.flash(i18n.F("flash.clipboard_unavailable", err))
			} else {
				m.flash(i18n.F("memory.copied", len([]rune(o.read.text))))
			}
		default:
			m.scrollKey(a)
		}
		return nil
	}
	n, page := len(o.view.entries()), max(1, m.ov.room)
	switch a := keyAct(inMemory, msg.String()); a {
	case actDown, actUp, actPageDown, actPageUp, actHalfDown, actHalfUp, actTop, actBottom:
		o.cursor = map[act]int{actDown: o.cursor + 1, actUp: o.cursor - 1, actPageDown: o.cursor + page, actPageUp: o.cursor - page,
			actHalfDown: o.cursor + max(1, page/2), actHalfUp: o.cursor - max(1, page/2), actTop: 0, actBottom: n - 1}[a]
		o.clamp()
		m.ov.focus = -1
	case actDelete:
		m.askTrashMemory()
	case actUndo:
		return m.doUndo()
	default:
		m.dialogKey(a, func() { m.readMemory(o.cursor) })
	}
	return nil
}

func (m *Model) memoryButtons() []btn {
	o := m.ov.mem
	read := btn{keyed(keyName("enter"), i18n.T("memory.btn_read")), true, nil}
	trash := btn{keyed(keyOf(inMemory, actDelete), i18n.T("memory.btn_trash")), false, nil}
	if len(o.view.entries()) > 0 {
		read.act = func(mm *Model) { mm.readMemory(mm.ov.mem.cursor) }
		if o.item().Kind == memory.KindClaude {
			trash.act = (*Model).askTrashMemory
		}
	}
	compare := btn{i18n.T("memory.btn_compare"), false, (*Model).askCompare}
	return []btn{read, trash, compare, {keyed(keyName("esc"), i18n.T("btn.close")), false, (*Model).closeOverlay}}
}

type memTextMsg struct {
	key  string
	seq  int
	text string
	at   time.Time
	why  string
}

// readMemory opens memory i of the list in the reader, read through memory.read on its machine.
func (m *Model) readMemory(i int) {
	o := m.ov.mem
	es := o.view.entries()
	if i < 0 || i >= len(es) {
		return
	}
	o.cursor, o.seq = i, o.seq+1
	e := es[i]
	o.read = &memRead{item: e, loading: true, seq: o.seq}
	m.ov.cursor = 0
	peer, key, seq := m.peerFn(), o.key, o.seq
	m.pending = tea.Batch(m.pending, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := memTextMsg{key: key, seq: seq}
		p, err := peer(ctx, e.Machine)
		if err != nil {
			msg.why = err.Error()
			return msg
		}
		t, err := remote.ReadMemory(ctx, p, e.Item)
		if err != nil {
			msg.why = remote.MemoryRefusal(p, hostLabel(e.Machine), err)
			return msg
		}
		msg.text, msg.at = t.Text, t.At
		return msg
	})
}

func (msg memTextMsg) apply(m *Model) tea.Cmd {
	if m.ov.kind != ovMemory || m.ov.mem.key != msg.key || m.ov.mem.read == nil || m.ov.mem.read.seq != msg.seq {
		return nil
	}
	r := m.ov.mem.read
	r.loading, r.text, r.at, r.why = false, msg.text, msg.at, msg.why
	return nil
}

// askTrashMemory asks before the Claude memory under the cursor goes into its machine's trash; Codex's are its own.
func (m *Model) askTrashMemory() {
	o := m.ov.mem
	if len(o.view.entries()) == 0 {
		return
	}
	e := o.item()
	if e.Kind != memory.KindClaude {
		m.flash(i18n.T("memory.codex_read_only"))
		return
	}
	if why := m.memoryWhy(e.Machine, remote.MMemoryTrash); why != "" {
		m.flash(why)
		return
	}
	back := func(mm *Model) { mm.ov = overlay{kind: ovMemory, focus: -1, mem: o} }
	lines := []string{render.Truncate(e.Title, 70), dimmed.Render(render.Truncate(m.memPath(e.Machine, e.File), 70)), "",
		i18n.F("memory.trash_body", m.memMachineLabel(e.Machine))}
	m.openConfirm(i18n.T("memory.trash_title"), i18n.T("memory.btn_trash"), lines, func(mm *Model) {
		back(mm)
		mm.pending = tea.Batch(mm.pending, mm.trashMemory(o.key, e))
	}, back)
}

type memTrashMsg struct {
	key   string
	e     memEntry
	entry string
	why   string
	old   bool
}

func (m *Model) trashMemory(key string, e memEntry) tea.Cmd {
	peer := m.peerFn()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := memTrashMsg{key: key, e: e}
		p, err := peer(ctx, e.Machine)
		if err != nil {
			msg.why = err.Error()
			return msg
		}
		var res remote.MemoryEntry
		if err := p.Call(ctx, remote.MMemoryTrash, remote.MemoryFile{File: e.File}, &res); err != nil {
			msg.why, msg.old = remote.MemoryRefusal(p, hostLabel(e.Machine), err), wire.Code(err) == wire.CodeUnknownMethod
			return msg
		}
		msg.entry = res.Entry
		return msg
	}
}

// apply offers the undo and reads the list again.
func (msg memTrashMsg) apply(m *Model) tea.Cmd {
	if msg.old {
		m.lackOn(msg.e.Machine, remote.MMemoryTrash)
	}
	if msg.why != "" {
		m.flash(msg.why)
		return nil
	}
	key, e, entry := msg.key, msg.e, msg.entry
	m.offerUndo(i18n.F("memory.trashed", e.Title), func(mm *Model) tea.Cmd { return mm.restoreMemory(key, e, entry) })
	return m.rereadMemories(key)
}

func (m *Model) rereadMemories(key string) tea.Cmd {
	if v := m.mems[key]; v != nil {
		return m.readMemories(v)
	}
	return nil
}

type memRestoreMsg struct {
	key string
	e   memEntry
	why string
}

// restoreMemory puts the trashed memory back through memory.restore on its machine.
func (m *Model) restoreMemory(key string, e memEntry, entry string) tea.Cmd {
	if why := m.memoryWhy(e.Machine, remote.MMemoryRestore); why != "" {
		m.flash(why)
		return nil
	}
	peer := m.peerFn()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		msg := memRestoreMsg{key: key, e: e}
		p, err := peer(ctx, e.Machine)
		if err != nil {
			msg.why = err.Error()
			return msg
		}
		var res remote.MemoryFile
		if err := p.Call(ctx, remote.MMemoryRestore, remote.MemoryEntry{Entry: entry}, &res); err != nil {
			msg.why = remote.MemoryRefusal(p, hostLabel(e.Machine), err)
		}
		return msg
	}
}

func (msg memRestoreMsg) apply(m *Model) tea.Cmd {
	if msg.why != "" {
		m.flash(msg.why)
		return nil
	}
	m.flash(i18n.F("undo.done", msg.e.Title))
	return m.rereadMemories(msg.key)
}

// memPath is file as its machine's user knows it: this machine's under ~.
func (m *Model) memPath(machine, file string) string {
	if machine == "" {
		return paths.Tilde(file)
	}
	return file
}

// memoryBlock is the project block's memory line, one per machine when the group has several or another's.
func (m *Model) memoryBlock(v *memView, inner, labelW int) []string {
	named := len(v.machines) > 1 || len(v.machines) == 1 && v.machines[0].machine != ""
	machW := 0
	for _, ms := range v.machines {
		machW = max(machW, render.Width(m.memMachineLabel(ms.machine)))
	}
	var out []string
	for i, ms := range v.machines {
		label := ""
		if i == 0 {
			label = render.GlyphBrand + " " + i18n.T("memory.label")
		}
		val := m.memCounts(ms)
		if named {
			val = render.Pad(m.memMachineLabel(ms.machine), machW) + "  " + val
		}
		out = append(out, dimmed.Render(render.Pad(label, labelW))+fit(val, inner-labelW))
	}
	return out
}

// memCounts: how many Claude memories and Codex blocks for these directories a machine has, and whether its index is
// over what Claude loads; or why it is not known.
func (m *Model) memCounts(ms *memSets) string {
	switch {
	case ms.why != "":
		return dimmed.Render(ms.why)
	case ms.sets == nil:
		return dimmed.Render(i18n.T("memory.loading"))
	}
	claude, codex, over := 0, 0, false
	for _, s := range ms.sets {
		switch {
		case s.Kind == memory.KindClaude:
			claude += len(s.Items)
			over = over || s.Over
		case s.Dir != "":
			codex += len(s.Items)
		}
	}
	if claude+codex == 0 && !over {
		return dimmed.Render(i18n.T("memory.none"))
	}
	text := i18n.F("memory.counts", claude, codex)
	if over {
		text += "  " + warnSty.Render(render.GlyphWarn+" "+i18n.T("memory.over"))
	}
	return text
}

// memLine is one line of the overlay's list; entry is the memory it shows, -1 for a heading.
type memLine struct {
	text  string
	entry int
}

func (m *Model) memoryLines(o *memOverlay, inner int) []memLine {
	var out []memLine
	n := 0
	for i, ms := range o.view.machines {
		if i > 0 {
			out = append(out, memLine{"", -1})
		}
		label := m.memMachineLabel(ms.machine)
		dirs := make([]string, len(ms.dirs))
		for j, d := range ms.dirs {
			dirs[j] = m.memPath(ms.machine, d)
		}
		out = append(out, memLine{accent.Render(label) + "  " + dimmed.Render(render.Truncate(strings.Join(dirs, ", "), max(4, inner-render.Width(label)-2))), -1})
		switch {
		case ms.why != "":
			for _, l := range render.Wrap(ms.why, inner-2) {
				out = append(out, memLine{"  " + warnSty.Render(l), -1})
			}
			continue
		case ms.sets == nil:
			out = append(out, memLine{"  " + dimmed.Render(i18n.T("memory.loading")), -1})
			continue
		case !slices.ContainsFunc(ms.sets, func(s memory.Set) bool { return len(s.Items) > 0 }):
			out = append(out, memLine{"  " + dimmed.Render(i18n.T("memory.empty")), -1})
			continue
		}
		for _, s := range ms.sets {
			if len(s.Items) == 0 {
				continue
			}
			head := ""
			switch {
			case s.Kind == memory.KindClaude:
				head = dimmed.Render(i18n.F("memory.set_claude", len(s.Items), s.Lines))
				if s.Over {
					head += "  " + warnSty.Render(render.GlyphWarn+" "+i18n.T("memory.over"))
				}
			case s.Dir != "":
				head = dimmed.Render(i18n.F("memory.set_codex", len(s.Items)))
			default:
				head = dimmed.Render(i18n.F("memory.set_loose", len(s.Items)))
			}
			out = append(out, memLine{"  " + head, -1})
			for _, it := range s.Items {
				out = append(out, memLine{m.memItemLine(it, s.Kind, n == o.cursor, inner), n})
				n++
			}
		}
	}
	return out
}

// memItemLine: title — description · age, and whether MEMORY.md lists a Claude memory.
func (m *Model) memItemLine(it memory.Item, kind string, sel bool, inner int) string {
	when := render.When(it.At, m.now)
	text := i18n.F("memory.item_bare", it.Title, when)
	if it.Description != "" {
		text = i18n.F("memory.item", it.Title, it.Description, when)
	}
	note := ""
	if kind == memory.KindClaude && !it.InIndex {
		note = "  " + i18n.T("memory.not_indexed")
	}
	lead := render.GlyphArrow + " "
	room := inner - 2 - render.Width(lead) - render.Width(note)
	text = render.Truncate(text, room)
	text += strings.Repeat(" ", max(0, room-render.Width(text)))
	if sel {
		return "  " + selTitle.Render(lead+text) + dimmed.Background(cSelBg).Render(note)
	}
	return "  " + strings.Repeat(" ", render.Width(lead)) + text + dimmed.Render(note)
}

func (m *Model) renderMemory() string {
	o := m.ov.mem
	if o.read != nil {
		return m.renderMemoryRead()
	}
	w := m.ovWidth()
	inner := w - 4
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(i18n.F("memory.title", o.view.label), inner)), ""}
	lines := m.memoryLines(o, inner)
	if len(o.view.machines) == 0 {
		lines = []memLine{{dimmed.Render(i18n.T("memory.no_dirs")), -1}}
	}
	room := max(3, m.h-4-len(body)-6)
	sel := slices.IndexFunc(lines, func(l memLine) bool { return l.entry == o.cursor })
	if sel >= 0 {
		o.top = min(max(o.top, sel-room+1), sel)
	}
	o.top = min(max(o.top, 0), max(0, len(lines)-room))
	m.ov.room = room
	for i := o.top; i < min(len(lines), o.top+room); i++ {
		if idx := lines[i].entry; idx >= 0 {
			m.mark(len(body)+1, ovPad, inner, func(mm *Model) {
				if mm.ov.mem.cursor == idx {
					mm.readMemory(idx)
					return
				}
				mm.ov.mem.cursor, mm.ov.focus = idx, -1
			})
		}
		body = append(body, lines[i].text)
	}
	if len(lines) > room {
		body = append(body, dimmed.Render(i18n.F("overlay.line_range", o.top+1, min(len(lines), o.top+room), len(lines))))
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, m.memoryButtons())...)
	return ovRender(body, w)
}

func (m *Model) renderMemoryRead() string {
	r := m.ov.mem.read
	w := max(44, m.w-8)
	inner := w - 4
	kind := i18n.T("memory.kind_claude")
	file := filepath.Base(r.item.File)
	if r.item.Kind != memory.KindClaude {
		kind, file = i18n.T("memory.kind_codex"), file+":"+strconv.Itoa(r.item.Line)
	}
	at := cmp.Or(r.at, r.item.At)
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(r.item.Title, inner)),
		dimmed.Render(render.Truncate(i18n.F("memory.read_facts", m.memMachineLabel(r.item.Machine), kind, file, render.When(at, m.now)), inner)),
		frame.Render(strings.Repeat(hRule, inner))}
	room := max(1, m.h-4-len(body)-4)
	var lines []string
	switch {
	case r.loading:
		lines = []string{dimmed.Render(i18n.T("memory.loading"))}
	case r.why != "":
		lines = render.Wrap(r.why, inner-2)
	default:
		lines = render.Wrap(strings.TrimRight(r.text, "\n"), inner-2)
	}
	end := m.scrollWindow(len(lines), room)
	bar := scrollbar(len(lines), m.ov.cursor, room)
	for i := m.ov.cursor; i < end; i++ {
		body = append(body, fit(lines[i], inner-2)+" "+bar[i-m.ov.cursor])
	}
	for len(body) < room+3 {
		body = append(body, "")
	}
	hint := i18n.F("memory.read_hint", keyName("shift+tab"), keyName("tab"), keyOf(inReader, actCopy), keyName("esc"))
	if len(lines) > room {
		hint = i18n.F("msg.hint_scroll", hint)
	}
	body = append(body, "", dimmed.Render(hint))
	return ovRender(body, w)
}

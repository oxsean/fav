package tui

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// The 「项目」 dialog, e on a group header of the projects view: an automatic group goes into a project (one picked or
// a new one) by the directories ticked; a project group is renamed, loses a directory, gains one on this machine.
// Every change goes through the coordinator (project.create / attach / detach / edit).

// The parts of the dialog Tab moves between.
const (
	partTop     = iota // filing: the projects; a project: its name
	partDirs           // the directories
	partButtons        // the buttons
)

type projDialog struct {
	group   string // the group's key
	id      string // the project shown; "" files an automatic group
	label   string // the group's name
	blocked string // why nothing can be changed (offline, an old server, a reader); "" when it can
	note    string // what this viewer cannot do (a participant)
	rename  bool   // the name may be changed
	part    int
	choices []*task.Project // filing: the projects the directories may go into; pick == len(choices) is a new one
	pick    int
	dirs    []projDir
	dirCur  int
	name    textinput.Model
	nRecs   int
}

type projDir struct {
	machine, dir, remote string
	n                    int  // sessions there
	ticked               bool // filing: it goes into the project
}

func (m *Model) onGroupHeader() bool {
	return m.view == viewProjects && m.current() == nil && m.groupUnderCursor() != ""
}

// openProject opens the dialog on the group under the cursor; mode 2 dials again when it is offline.
func (m *Model) openProject() tea.Cmd {
	g := m.groupUnderCursor()
	if g == "" {
		return nil
	}
	return m.openProjectOn(g)
}

// openProjectOn opens the dialog on group g, which a project left without directories no longer lists.
func (m *Model) openProjectOn(g string) tea.Cmd {
	s := &m.proj.snap
	d := &projDialog{group: g, id: groupProject(g), label: m.groupLabel(g), nRecs: len(m.groups[g])}
	switch s.State {
	case projects.Offline:
		d.blocked = i18n.T("project.offline")
	case projects.Outdated:
		d.blocked = i18n.T("project.old_server")
	}
	d.name = newInput()
	d.name.Prompt, d.name.CharLimit = "", 64
	if d.id == "" {
		m.fileDialog(d)
	} else {
		m.editDialog(d)
	}
	m.ov = overlay{kind: ovProject, focus: -1, proj: d}
	m.focusProjectInput()
	if m.served() && s.State == projects.Offline && !m.tasks.connecting {
		return m.tasksOpen()
	}
	return nil
}

// fileDialog lists the group's directories, most used first and that one ticked, and the projects they may go into.
func (m *Model) fileDialog(d *projDialog) {
	s := &m.proj.snap
	at := map[[2]string]int{}
	for _, r := range m.groups[d.group] {
		mc, dir := s.Machine(r), projects.Dir(r)
		if mc == "" || dir == "" {
			continue
		}
		k := [2]string{mc, dir}
		i, ok := at[k]
		if !ok {
			i, at[k] = len(d.dirs), len(d.dirs)
			d.dirs = append(d.dirs, projDir{machine: mc, dir: dir})
		}
		d.dirs[i].n++
		d.dirs[i].remote = cmp.Or(d.dirs[i].remote, r.GitRemote)
	}
	slices.SortStableFunc(d.dirs, func(a, b projDir) int { return b.n - a.n })
	if len(d.dirs) > 0 {
		d.dirs[0].ticked = true
		d.choices = s.Attachable(d.dirs[0].machine)
	}
	d.name.SetValue(d.label)
}

// editDialog shows the project's directories, this machine's first, and what the viewer may change.
func (m *Model) editDialog(d *projDialog) {
	s := &m.proj.snap
	p := s.Projects[d.id]
	if p == nil {
		d.blocked = cmp.Or(d.blocked, i18n.T("project.offline"))
		return
	}
	d.label = p.Name
	d.name.SetValue(p.Name)
	for _, r := range p.Repos {
		for mc, dir := range r.Dirs {
			d.dirs = append(d.dirs, projDir{machine: mc, dir: dir, remote: r.Remote})
		}
	}
	slices.SortStableFunc(d.dirs, func(a, b projDir) int {
		if (a.machine == s.Here) != (b.machine == s.Here) {
			if a.machine == s.Here {
				return -1
			}
			return 1
		}
		return cmp.Or(strings.Compare(a.machine, b.machine), strings.Compare(a.dir, b.dir))
	})
	v := s.Viewer
	d.rename = v.Admin || p.Owner == v.User
	switch {
	case d.blocked != "":
	case !v.Admin && p.Role(v.User) == task.RoleReader:
		d.blocked = i18n.F("project.reader", p.Name)
	case !d.rename:
		d.note = i18n.T("project.participant")
	}
	if d.blocked == "" && !d.rename {
		d.part = partDirs
	}
}

// focusProjectInput focuses the name box where the part shown takes text.
func (m *Model) focusProjectInput() {
	d := m.ov.proj
	if d.typing() {
		d.name.Focus()
		d.name.CursorEnd()
	} else {
		d.name.Blur()
	}
}

// typing: the name box takes the keys.
func (d *projDialog) typing() bool {
	if d.blocked != "" || d.part != partTop {
		return false
	}
	if d.id == "" {
		return d.pick == len(d.choices)
	}
	return d.rename
}

func (d *projDialog) ticked() []projDir {
	var out []projDir
	for _, x := range d.dirs {
		if x.ticked {
			out = append(out, x)
		}
	}
	return out
}

func (m *Model) machineLabel(name string) string {
	if name != "" && name == m.proj.snap.Here {
		return i18n.T("project.this_machine")
	}
	return name
}

func (m *Model) projectButtons() []btn {
	d := m.ov.proj
	closeBtn := btn{keyed(keyName("esc"), i18n.T("btn.close")), false, (*Model).closeOverlay}
	if d.blocked != "" {
		closeBtn.primary = true
		return []btn{closeBtn}
	}
	if d.id == "" {
		label := i18n.F("project.btn_file", render.Truncate(d.target(), 24))
		if d.pick == len(d.choices) {
			label = i18n.T("project.btn_create")
		}
		return []btn{{keyed(keyName("enter"), label), true, func(mm *Model) { mm.pending = tea.Batch(mm.pending, mm.fileProject()) }},
			cancelBtn()}
	}
	s := &m.proj.snap
	p := s.Projects[d.id]
	var bs []btn
	if s.Here != "" && s.MayAttach(p, s.Here) {
		bs = append(bs, btn{i18n.T("project.btn_add_dir"), false, (*Model).askAttachDir})
	}
	if d.dirCur < len(d.dirs) {
		if x := d.dirs[d.dirCur]; s.MayAttach(p, x.machine) {
			bs = append(bs, btn{i18n.T("project.btn_detach"), false, func(mm *Model) { mm.pending = tea.Batch(mm.pending, mm.detachDir()) }})
		} else {
			bs = append(bs, btn{i18n.T("project.btn_detach_not_yours"), false, func(mm *Model) { mm.flash(i18n.T("project.participant")) }})
		}
	}
	if d.rename {
		return append(bs, btn{keyed(keyName("enter"), i18n.T("project.btn_save")), true, func(mm *Model) { mm.pending = tea.Batch(mm.pending, mm.renameProject()) }}, closeBtn)
	}
	closeBtn.primary = true
	return append(bs, closeBtn)
}

// target is the project the directories go into.
func (d *projDialog) target() string {
	if d.pick < len(d.choices) {
		return d.choices[d.pick].Name
	}
	return strings.TrimSpace(d.name.Value())
}

func (m *Model) renderProject() string {
	d := m.ov.proj
	s := &m.proj.snap
	w := m.ovWidth()
	inner := w - 4
	var body []string
	line := func(text string, act func(*Model)) {
		if act != nil {
			m.mark(len(body)+1, ovPad, inner, act)
		}
		body = append(body, fit(text, inner))
	}
	tick := keyOf(inProject, actTick)
	if d.id == "" {
		body = append(body, boldSty.Foreground(cText).Render(i18n.T("project.file_title")))
		body = append(body, dimmed.Render(render.Truncate(i18n.F("project.file_sub", d.label, d.nRecs, len(d.dirs)), inner)))
		if d.blocked == "" {
			body = append(body, dimmed.Render(render.Truncate(i18n.F("project.file_keys", tick), inner)))
		}
	} else {
		body = append(body, boldSty.Foreground(cText).Render(render.Truncate(i18n.F("project.edit_title", d.label), inner)))
		if p := s.Projects[d.id]; p != nil {
			body = append(body, dimmed.Render(render.Truncate(m.projectKindLine(p, true), inner)))
		}
		switch {
		case d.note != "":
			body = append(body, dimmed.Render(render.Truncate(d.note, inner)))
		case d.blocked == "":
			body = append(body, dimmed.Render(render.Truncate(i18n.T("project.edit_keys"), inner)))
		}
	}
	body = append(body, "")
	if d.blocked != "" {
		body = append(body, warnSty.Render(render.Truncate(render.GlyphWarn+" "+d.blocked, inner)), "")
	}
	if d.blocked == "" {
		m.renderProjectTop(&body, line, inner)
	}
	if d.id == "" {
		body = append(body, accent.Render(i18n.T("project.dirs_pick")))
	} else {
		body = append(body, accent.Render(i18n.T("project.dirs_label")))
	}
	if len(d.dirs) == 0 {
		body = append(body, dimmed.Render("  "+i18n.T("project.no_dirs")))
	}
	machW := 0
	for _, x := range d.dirs {
		machW = max(machW, render.Width(m.machineLabel(x.machine)))
	}
	room := max(3, m.h-len(body)-12)
	first := min(max(0, d.dirCur-room+1), max(0, len(d.dirs)-room))
	for i := first; i < len(d.dirs) && i < first+room; i++ {
		x := d.dirs[i]
		cur := d.blocked == "" && i == d.dirCur && (d.part == partDirs || d.id != "")
		mark := "  "
		if cur {
			mark = render.GlyphClosed + " "
		}
		box := ""
		if d.id == "" {
			box = "[ ] "
			if x.ticked {
				box = "[+] "
			}
		}
		tail := ""
		switch {
		case d.id == "":
			tail = i18n.F("project.sessions_n", x.n)
		case x.machine == s.Here && paths.IsDir(x.dir):
			tail = i18n.T("project.here")
		case x.machine == s.Here:
			tail = errSty.Render(i18n.T("project.not_here"))
		case d.note != "" && s.Owner(x.machine) != "" && s.Owner(x.machine) != s.Viewer.User:
			tail = dimmed.Render(i18n.F("project.someones", m.nameOf(s.Owner(x.machine))))
		}
		head := mark + box + render.Pad(m.machineLabel(x.machine), machW) + "  "
		dir := render.Truncate(paths.Tilde(x.dir), max(8, inner-render.Width(head)-render.Width(tail)-2))
		text := head + dir + strings.Repeat(" ", max(1, inner-render.Width(head)-render.Width(dir)-render.Width(tail))) + tail
		if cur {
			text = accent.Bold(true).Render(head+dir) + strings.Repeat(" ", max(1, inner-render.Width(head)-render.Width(dir)-render.Width(tail))) + tail
		} else if d.blocked != "" {
			text = dimmed.Render(head+dir) + strings.Repeat(" ", max(1, inner-render.Width(head)-render.Width(dir)-render.Width(tail))) + tail
		}
		idx := i
		var act func(*Model)
		if d.blocked == "" {
			act = func(mm *Model) { mm.clickProjectDir(idx) }
		}
		line(text, act)
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, m.projectButtons())...)
	return ovRender(body, w)
}

// renderProjectTop: filing lists the projects (and the new one's name); a project shows its name box.
func (m *Model) renderProjectTop(body *[]string, line func(string, func(*Model)), inner int) {
	d := m.ov.proj
	input := func() {
		d.name.SetWidth(inner - 6)
		border := cFrame
		if d.name.Focused() {
			border = cAccent
		}
		m.mark(len(*body)+2, ovPad, inner, func(mm *Model) {
			mm.ov.proj.part = partTop
			mm.ov.focus = -1
			mm.focusProjectInput()
			mm.placeCursor(&mm.ov.proj.name, 2)
		})
		*body = append(*body, strings.Split(panelSty.BorderForeground(border).Width(inner).Render(fit(inputView(d.name), inner-4)), "\n")...)
	}
	if d.id != "" {
		*body = append(*body, accent.Render(i18n.T("project.name")))
		if d.rename {
			input()
		} else {
			*body = append(*body, "  "+d.label, dimmed.Render("  "+i18n.T("project.rename_owner_only")))
		}
		*body = append(*body, "")
		return
	}
	*body = append(*body, accent.Render(i18n.T("project.can_add")))
	nameW := 0
	for _, p := range d.choices {
		nameW = max(nameW, render.Width(p.Name)+2)
	}
	nameW = min(nameW, inner/2)
	for i := 0; i <= len(d.choices); i++ {
		cur := i == d.pick
		mark := "  "
		if cur {
			mark = render.GlyphClosed + " "
		}
		var text string
		if i < len(d.choices) {
			p := d.choices[i]
			text = mark + render.GlyphProject + " " + render.Pad(render.Truncate(p.Name, nameW), nameW) + "  " + dimmed.Render(m.projectKindLine(p, false))
			if cur {
				text = accent.Bold(true).Render(mark+render.GlyphProject+" "+render.Pad(render.Truncate(p.Name, nameW), nameW)) + "  " + dimmed.Render(m.projectKindLine(p, false))
			}
		} else {
			text = mark + i18n.T("project.new")
			if cur {
				text = accent.Bold(true).Render(text)
			}
		}
		idx := i
		line(text, func(mm *Model) { mm.pickProjectChoice(idx) })
	}
	if d.pick == len(d.choices) {
		input()
		*body = append(*body, dimmed.Render(render.Truncate(i18n.T("project.new_note"), inner)))
	}
	*body = append(*body, "")
}

// projectKindLine: team or personal, its owner and participants; long adds where members are changed.
func (m *Model) projectKindLine(p *task.Project, long bool) string {
	if projects.Personal(p) {
		return i18n.T("project.kind_personal")
	}
	if long {
		return i18n.F("project.edit_sub", m.nameOf(p.Owner), projects.Participants(p))
	}
	return i18n.F("project.kind_team", m.nameOf(p.Owner), projects.Participants(p))
}

func (m *Model) pickProjectChoice(i int) {
	d := m.ov.proj
	d.pick, d.part, m.ov.focus = i, partTop, -1
	m.focusProjectInput()
}

func (m *Model) clickProjectDir(i int) {
	d := m.ov.proj
	d.dirCur, d.part, m.ov.focus = i, partDirs, -1
	if d.id == "" {
		d.dirs[i].ticked = !d.dirs[i].ticked
	}
	m.focusProjectInput()
}

func (m *Model) projectKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.ov.proj
	k := msg.String()
	a := keyAct(inProject, k)
	if d.typing() && (isLetter(k) || a == actNone || a == actLeft || a == actRight) {
		var cmd tea.Cmd
		d.name, cmd = d.name.Update(msg)
		return cmd
	}
	switch a {
	case actFocusNext, actFocusPrev:
		m.stepProjectPart(map[act]int{actFocusNext: 1, actFocusPrev: -1}[a])
	case actDown, actUp:
		delta := map[act]int{actDown: 1, actUp: -1}[a]
		switch {
		case d.blocked != "":
		case d.part == partTop && d.id == "":
			d.pick = min(max(d.pick+delta, 0), len(d.choices))
			m.focusProjectInput()
		default:
			if d.part == partButtons {
				d.part, m.ov.focus = partDirs, -1
			}
			d.dirCur = min(max(d.dirCur+delta, 0), max(0, len(d.dirs)-1))
		}
	case actTick:
		if d.id == "" && d.blocked == "" && d.dirCur < len(d.dirs) {
			d.dirs[d.dirCur].ticked = !d.dirs[d.dirCur].ticked
		}
	case actLeft, actRight:
		if d.part == partButtons {
			m.moveFocus(map[act]int{actRight: 1, actLeft: -1}[a])
		}
	default:
		m.dialogKey(a, func() {
			for _, b := range m.projectButtons() {
				if b.primary {
					b.act(m)
					return
				}
			}
		})
	}
	return nil
}

// stepProjectPart moves to the next part that has something to do; the buttons part focuses the primary button.
func (m *Model) stepProjectPart(delta int) {
	d := m.ov.proj
	if d.blocked != "" {
		m.moveFocus(delta)
		return
	}
	parts := []int{partTop, partDirs, partButtons}
	if d.id != "" && !d.rename {
		parts = []int{partDirs, partButtons}
	}
	i := (slices.Index(parts, d.part) + delta + len(parts)) % len(parts)
	d.part, m.ov.focus = parts[i], -1
	if d.part == partButtons {
		m.moveFocus(0)
	}
	m.focusProjectInput()
}

type projDoneMsg struct {
	note  string
	group string // the group to show once it is done
	again bool   // open the dialog on it again
	err   error
}

func (msg projDoneMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		m.flash(i18n.F("tasks.failed", reasonText(msg.err)))
	}
	m.readProjects()
	if msg.err == nil {
		m.flash(msg.note)
	}
	if msg.group == "" {
		return nil
	}
	m.open[msg.group] = true
	m.refresh()
	for i, r := range m.rows {
		if r.group == msg.group {
			total := 0
			for _, h := range m.rowHeights() {
				total += h
			}
			m.cursor, m.scroll = i, min(m.scroll, max(0, total-m.listHeight())) // regrouping moved rows under the cursor
			m.ensureVisible()
			break
		}
	}
	if !msg.again {
		return nil
	}
	var was *projDialog
	switch {
	case m.ov.kind == ovProject && m.ov.proj.group == msg.group:
		was = m.ov.proj
	case m.ov.active() || m.groupUnderCursor() != msg.group: // closed meanwhile, or the picker's choice moved nothing
		return nil
	}
	cmd := m.openProjectOn(msg.group)
	if d := m.ov.proj; was != nil && d.blocked == "" && was.part != partTop {
		d.part, d.dirCur = partDirs, min(was.dirCur, max(0, len(d.dirs)-1))
		m.focusProjectInput()
	}
	return cmd
}

// fileProject puts the ticked directories into the project picked, creating it first when it is a new one.
func (m *Model) fileProject() tea.Cmd {
	d := m.ov.proj
	dirs := d.ticked()
	if len(dirs) == 0 {
		m.flash(i18n.T("project.none_ticked"))
		return nil
	}
	name := d.target()
	if name == "" {
		m.flash(i18n.T("project.no_name"))
		return nil
	}
	id := ""
	if d.pick < len(d.choices) {
		id = d.choices[d.pick].ID
	}
	m.closeOverlay()
	note := i18n.F("project.filed", len(dirs), name)
	return m.withCoord(func(m *Model) tea.Cmd {
		cl := m.tasks.cl
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
			defer cancel()
			if id == "" {
				var p task.Project
				if err := cl.CallCommand(ctx, coord.MProjectCreate, commandID(), coord.ProjectCreate{Name: name}, &p); err != nil {
					return projDoneMsg{err: err}
				}
				id = p.ID
			}
			for _, x := range dirs {
				err := cl.CallCommand(ctx, coord.MProjectAttach, commandID(), coord.ProjectAttach{Project: id, Machine: x.machine, Dir: x.dir, Remote: x.remote}, nil)
				if err != nil {
					return projDoneMsg{err: err, group: projKey + id}
				}
			}
			return projDoneMsg{note: note, group: projKey + id}
		}
	})
}

func (m *Model) renameProject() tea.Cmd {
	d := m.ov.proj
	name := strings.TrimSpace(d.name.Value())
	if name == "" {
		m.flash(i18n.T("project.no_name"))
		return nil
	}
	m.closeOverlay()
	if name == d.label {
		return nil
	}
	id := d.id
	return m.projectCall(coord.MProjectEdit, task.ProjectEdit{ID: id, Name: &name}, i18n.F("project.renamed", name), projKey+id, false)
}

// detachDir takes the directory under the cursor out of the project (undone by adding it again); the dialog stays.
func (m *Model) detachDir() tea.Cmd {
	d := m.ov.proj
	if d.dirCur >= len(d.dirs) {
		return nil
	}
	x := d.dirs[d.dirCur]
	return m.projectCall(coord.MProjectDetach, coord.ProjectDetach{Project: d.id, Machine: x.machine, Dir: x.dir},
		i18n.F("project.detached", paths.Tilde(x.dir)), d.group, true)
}

// askAttachDir picks a directory on this machine, then adds it to the project and opens the dialog again.
func (m *Model) askAttachDir() {
	d := m.ov.proj
	id, group, here := d.id, d.group, m.proj.snap.Here
	start := ""
	if home, err := os.UserHomeDir(); err == nil {
		start = paths.Tilde(home) + string(filepath.Separator)
	}
	for _, x := range d.dirs {
		if x.machine == here {
			start = x.dir
			break
		}
	}
	m.openDirPicker(i18n.F("project.add_dir_title", d.label), start, func(m *Model, dir string) {
		m.pending = tea.Batch(m.pending, m.projectCall(coord.MProjectAttach,
			coord.ProjectAttach{Project: id, Machine: here, Dir: dir, Remote: m.remoteOf(dir)}, i18n.F("project.attached", paths.Tilde(dir)), group, true))
	})
}

// remoteOf is the git remote of a session of this machine under dir.
func (m *Model) remoteOf(dir string) string {
	for _, rs := range [][]*tend.Rec{m.store.All(), m.unfav} {
		for _, r := range rs {
			if r.Host == "" && r.GitRemote != "" && paths.Nested(r.Cwd, dir) {
				return r.GitRemote
			}
		}
	}
	return ""
}

func (m *Model) projectCall(method string, params any, note, group string, again bool) tea.Cmd {
	return m.withCoord(func(m *Model) tea.Cmd {
		cl := m.tasks.cl
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
			defer cancel()
			err := cl.CallCommand(ctx, method, commandID(), params, nil)
			return projDoneMsg{note: note, group: group, again: again, err: err}
		}
	})
}

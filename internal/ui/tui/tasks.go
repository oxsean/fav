package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Connector reaches the coordinator: the running one, or this process becoming it.
type Connector func(wire.Options) (*coord.Client, error)

// tasksState is the Tasks view: the coordinator's state, the machines and the outputs shown, as their streams push them.
type tasksState struct {
	connect    Connector
	cl         *coord.Client
	connecting bool
	err        error // connecting failed; retried when the view is opened again
	st         *task.State
	loaded     bool
	machines   []coord.Machine
	machinesIn bool  // this connection listed the machines
	lost       error // mode 2: why the server could not be reached, until the next connection
	agents     []tend.AgentProfile
	list       []*task.Task // shown, filtered by the search box, in the layout's order
	layout     layout
	depth      map[string]int // the tree: how deep each task sits
	cols       [][]*task.Task // the board: the tasks of each column; list holds them column after column
	split      int            // the home: list[:split] wait for you, the rest run
	matched    int            // the tasks the query matches, whichever of them the layout shows
	cursor     int
	scroll     int
	out        map[string]*outFeed // by run id: the runs the view shows
	fold       coord.StateFold     // the state as state.watch brings it; st is its St
	gen        int                 // which opening of state.watch the pushes belong to
	stream     *wire.Watch         // the current state.watch
	mstream    *wire.Watch         // the current machines.watch
	editing    *editing            // what $EDITOR has open
	anchor     string              // where a range of marked tasks started
	watch      string              // a run shown under the detail, whichever task is selected
	marked     map[string]bool     // the range: tasks an action applies to together
	unsaved    map[string][]byte   // edited text the coordinator did not take, by edit key; the next edit starts from it
	askPick    int                 // the home layout's inline answer: the option highlighted for its question
	askDeny    bool                // it is composing a deny reason
	askReason  textinput.Model
	editsOpen  bool                  // the output's edit rows show their first hunks
	openEdits  map[string]bool       // edit rows clicked the other way, by run and row key
	links      map[string]*task.Task // by session id: the task its newest run worked for (taskOf)
	linkedAt   linkKey
	treesDone  map[string]time.Time // the trees the state stream says are done, by when; nil before its first state
}

const tasksWait = 20 * time.Second

// SetCoordinator lets the Tasks view reach the coordinator; without it the view says tasks are unavailable.
func (m *Model) SetCoordinator(connect Connector) { m.tasks.connect = connect }

// CloseCoordinator ends the connection; a TUI that became the coordinator gives up the lock (runs keep going).
func (m *Model) CloseCoordinator() {
	if m.tasks.cl != nil {
		m.tasks.cl.CloseNow()
		m.tasks.cl = nil
	}
}

type tasksConnMsg struct {
	cl  *coord.Client
	err error
}

// taskDoneMsg: a command came back; then runs on success.
// taskLoadedMsg: task.get answered; the edit form opens on the whole task.
type taskLoadedMsg struct {
	t   *task.Task
	err error
}

func (msg taskLoadedMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		m.flash(reasonText(msg.err))
		return nil
	}
	return m.openTaskForm(msg.t)
}

// editTask reads the selected task whole (the list may lack its brief) and then opens the form on it.
func (m *Model) editTask() tea.Cmd {
	x, cl := m.selectedTask(), m.tasks.cl
	if x == nil {
		return nil
	}
	if cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	id := x.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		t := &task.Task{}
		err := cl.Call(ctx, coord.MTaskGet, task.RunRef{ID: id}, t)
		return taskLoadedMsg{t: t, err: err}
	}
}

type taskDoneMsg struct {
	note string
	err  error
	then func(*Model) tea.Cmd
}

// tasksOpen starts connecting when the Tasks view shows.
func (m *Model) tasksOpen() tea.Cmd {
	t := &m.tasks
	if t.connect == nil || t.cl != nil || t.connecting {
		return nil
	}
	t.connecting, t.err = true, nil
	connect := t.connect
	return func() tea.Msg {
		cl, err := connect(wire.Options{})
		return tasksConnMsg{cl, err}
	}
}

// openMachines opens machines.watch; a coordinator without it is asked machine.list once.
func (m *Model) openMachines() tea.Cmd {
	t := &m.tasks
	if t.mstream != nil {
		t.mstream.Cancel()
		t.mstream = nil
	}
	cl := t.cl
	return func() tea.Msg {
		s := nextState(cl, cl.Watch(context.Background(), coord.MMachinesWatch, coord.TopicParams{}), 0)
		return machinesPushMsg{cl: cl, w: s.w, pushes: s.pushes, err: s.err}
	}
}

type machinesPushMsg struct {
	cl     *coord.Client
	w      *wire.Watch
	pushes []wire.Push
	err    error // the stream ended
}

func (msg machinesPushMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	if t.cl != msg.cl || t.mstream != nil && t.mstream != msg.w {
		msg.w.Cancel()
		return nil
	}
	t.mstream = msg.w
	for _, p := range msg.pushes {
		var ml coord.MachineList
		if p.Method == coord.PushMachines && p.Decode(&ml) == nil {
			t.machines, t.machinesIn = ml.Items, true
		}
	}
	m.syncServed()
	synced := m.syncMachines()
	switch {
	case msg.err == nil:
		return tea.Batch(synced, func() tea.Msg {
			s := nextState(msg.cl, msg.w, 0)
			return machinesPushMsg{cl: msg.cl, w: msg.w, pushes: s.pushes, err: s.err}
		})
	case wire.Code(msg.err) == wire.CodeUnknownMethod:
		t.mstream = nil
		return tea.Batch(synced, m.listMachines())
	case wire.Code(msg.err) == wire.CodeLagged:
		return tea.Batch(synced, m.openMachines())
	}
	tracef("tasks: machines watch ended: %v", msg.err)
	t.mstream = nil
	return synced
}

// listMachines reads the machines once, and the agents while they are not known.
func (m *Model) listMachines() tea.Cmd {
	cl := m.tasks.cl
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var msg tasksMachinesMsg
		var ms coord.Machines
		if msg.err = cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms); msg.err == nil {
			msg.machines = ms.Machines
		}
		return msg
	}
}

// readAgents reads the agents one can run, once per connection.
func (m *Model) readAgents() tea.Cmd {
	cl := m.tasks.cl
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var msg tasksMachinesMsg
		var as coord.Agents
		msg.err = cl.Call(ctx, coord.MAgentList, nil, &as)
		msg.agents, msg.onlyAgents = as.Agents, true
		return msg
	}
}

type tasksMachinesMsg struct {
	machines   []coord.Machine
	agents     []tend.AgentProfile
	onlyAgents bool
	err        error
}

func (msg tasksMachinesMsg) apply(m *Model) tea.Cmd {
	var synced tea.Cmd
	if msg.err == nil && !msg.onlyAgents {
		m.tasks.machines, m.tasks.machinesIn = msg.machines, true
		m.syncServed()
		synced = m.syncMachines()
	}
	if len(msg.agents) > 0 {
		m.tasks.agents = msg.agents
		m.syncMakeAgents()
	}
	return synced
}

// openState opens state.watch from what the fold holds: a resume after its seq, or a snapshot.
func (m *Model) openState() tea.Cmd {
	t := &m.tasks
	if t.stream != nil {
		t.stream.Cancel()
		t.stream = nil
	}
	t.gen++
	cl, gen, params := t.cl, t.gen, t.fold.Params(false)
	return func() tea.Msg {
		return nextState(cl, cl.Watch(context.Background(), coord.MStateWatch, params), gen)
	}
}

// nextState waits for the next pushes of w, all that are there, or its end.
func nextState(cl *coord.Client, w *wire.Watch, gen int) tasksPushMsg {
	msg := tasksPushMsg{cl: cl, w: w, gen: gen}
	p, err := w.Next(context.Background())
	if err != nil {
		msg.err = err
		return msg
	}
	msg.pushes = append(msg.pushes, p)
	now, cancel := context.WithCancel(context.Background())
	cancel()
	for len(msg.pushes) < 512 {
		p, err := w.Next(now)
		if errors.Is(err, context.Canceled) {
			break
		}
		if err != nil {
			msg.err = err
			break
		}
		msg.pushes = append(msg.pushes, p)
	}
	return msg
}

type tasksPushMsg struct {
	cl     *coord.Client
	w      *wire.Watch
	gen    int
	pushes []wire.Push
	err    error // the stream ended
}

// apply folds the pushes into the state; a push the copy cannot take opens the stream again for a snapshot, and one
// that fell behind opens it again from the copy.
func (msg tasksPushMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	if t.cl != msg.cl || t.gen != msg.gen {
		return nil
	}
	t.stream = msg.w
	changed := false
	for _, p := range msg.pushes {
		c, err := t.fold.Apply(p)
		if err != nil {
			tracef("tasks: state push %s refused: %v", p.Method, err)
			t.fold.Reset()
			return m.openState()
		}
		changed = changed || c
	}
	if changed {
		t.err, t.st, t.loaded = nil, t.fold.St, true
		m.filterTasks()
		m.announceTreesDone()
		m.syncServed()
	}
	switch {
	case msg.err == nil:
		return func() tea.Msg { return nextState(msg.cl, msg.w, msg.gen) }
	case closed(t.cl.Done()): // the coordinator went away, keepalive: connect again while the view shows
		t.stream, t.mstream = nil, nil
		t.cl.Close()
		t.cl = nil
		m.syncServed()
		if m.served() { // the lists read through it too: dial again whatever the view
			m.serverLost(&wire.Error{Code: wire.CodeClosed})
			return m.tasksOpen()
		}
		if m.view == viewTasks {
			return m.tasksOpen()
		}
		return nil
	case wire.Code(msg.err) == wire.CodeLagged:
		tracef("tasks: state watch lagged at %d", t.fold.Params(false).AfterSeq)
		return m.openState()
	}
	t.stream = nil
	t.err = msg.err
	return nil
}

// announceTreesDone tells at the bottom of each tree the state stream says came to be done since the last look; the
// first look only sees what is done already.
func (m *Model) announceTreesDone() {
	t := &m.tasks
	now := map[string]time.Time{}
	for id, a := range t.fold.Aff.Tasks {
		if a != nil && a.TreeDone != nil {
			now[id] = a.TreeDone.DoneAt
		}
	}
	if t.treesDone != nil {
		for _, id := range slices.Sorted(maps.Keys(now)) {
			if was, ok := t.treesDone[id]; ok && was.Equal(now[id]) {
				continue
			}
			s, title := t.fold.Aff.Tasks[id].TreeDone, id
			if x := t.st.Tasks[id]; x != nil {
				title = render.OneLine(x.Title)
			}
			m.flash(render.GlyphDone + i18n.F("tasks.tree_done_flash", render.Truncate(title, 40), s.Done, s.Leaves))
		}
	}
	t.treesDone = now
}

// treeDoneLine is how a tree done went, in one line.
func treeDoneLine(s task.TreeSummary) string {
	parts := []string{i18n.F("tasks.tree_done_line", s.Done, s.Leaves, s.Runs)}
	if s.Canceled > 0 {
		parts = append(parts, i18n.F("tasks.tree_done_canceled", s.Canceled))
	}
	if !s.Started.IsZero() && s.DoneAt.After(s.Started) {
		d := s.DoneAt.Sub(s.Started)
		m := int(d / time.Minute)
		span := fmt.Sprintf("%dm", m)
		switch {
		case m == 0:
			span = d.Round(time.Second).String()
		case m >= 60:
			span = fmt.Sprintf("%dh %02dm", m/60, m%60)
		}
		parts = append(parts, i18n.F("tasks.tree_done_took", span))
	}
	if s.Branch != "" {
		parts = append(parts, i18n.F("tasks.tree_done_branch", render.OneLine(s.Branch)))
	}
	return strings.Join(parts, " · ")
}

func (msg tasksConnMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	t.connecting = false
	if msg.err != nil {
		t.err = msg.err
		if m.served() {
			m.serverLost(msg.err)
			return tea.Batch(m.connected(msg.err), m.retryServer())
		}
		return m.connected(msg.err)
	}
	t.cl, m.proj.hello, t.machinesIn, t.lost, m.far.retries = msg.cl, nil, false, nil, 0
	return tea.Batch(m.openState(), m.openMachines(), m.readAgents(), m.readHello(), m.connected(nil))
}

func (msg taskDoneMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		m.flash(i18n.F("tasks.failed", reasonText(msg.err)))
		return nil
	}
	if msg.note != "" {
		m.flash(msg.note)
	}
	if msg.then != nil {
		return msg.then(m)
	}
	return nil
}

// reasonText: a protocol error as a short localized phrase with its detail.
func reasonText(err error) string {
	var detail string
	if e, ok := err.(*wire.Error); ok {
		detail = e.Detail
	}
	r := remote.Reason(err)
	if detail != "" {
		return r + " — " + render.OneLine(detail)
	}
	return r
}

// write sends a command with a fresh id; note is flashed when it succeeds.
func (m *Model) write(method string, params any, note string, then func(*Model) tea.Cmd) tea.Cmd {
	return m.writeAs(commandID(), method, params, note, then)
}

// writeAs is write as command id, which task.undo names to take it back.
func (m *Model) writeAs(id, method string, params any, note string, then func(*Model) tea.Cmd) tea.Cmd {
	cl := m.tasks.cl
	if cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		err := cl.CallCommand(ctx, method, id, params, nil)
		return taskDoneMsg{note: note, err: err, then: then}
	}
}

func commandID() string {
	var b [8]byte
	rand.Read(b[:])
	return "tui-" + hex.EncodeToString(b[:])
}

// parseTaskFilter splits the search box into plain text and its project:<id>, stage:<name> and needs:you terms.
func parseTaskFilter(raw string) (q, project, stage string, needsYou bool) {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(strings.TrimSpace(raw))) {
		switch {
		case strings.HasPrefix(w, "project:"):
			project = strings.TrimPrefix(w, "project:")
		case strings.HasPrefix(w, "stage:"):
			stage = strings.TrimPrefix(w, "stage:")
		case w == "needs:you":
			needsYou = true
		default:
			words = append(words, w)
		}
	}
	return strings.Join(words, " "), project, stage, needsYou
}

// filterTasks: open tasks (and done ones whose run is still open) first, newest first; the search box filters by text.
func (m *Model) filterTasks() {
	t := &m.tasks
	if t.st == nil {
		return
	}
	var cur string
	if s := m.selectedTask(); s != nil {
		cur = s.ID
	}
	q, project, stage, needsYou := parseTaskFilter(m.search.Value())
	match := func(x *task.Task) bool {
		switch {
		case project != "" && strings.ToLower(x.Project) != project:
			return false
		case stage != "" && strings.ToLower(x.Stage) != stage:
			return false
		case needsYou && !m.needsYou(x):
			return false
		}
		return q == "" || strings.Contains(strings.ToLower(x.Title+"\n"+x.Brief+"\n"+x.Dir), q)
	}
	var needs, open, closed []*task.Task
	for _, r := range t.st.NeedsYou() { // longest waiting first
		if x := t.st.Tasks[r.Task]; x != nil && match(x) {
			needs = append(needs, x)
		}
	}
	for _, x := range t.st.Sorted() {
		switch {
		case !match(x) || m.needsYou(x):
		case x.Status == task.StatusTodo || t.st.Running(x.ID):
			open = append(open, x)
		default:
			closed = append(closed, x)
		}
	}
	m.arrange(needs, open, closed)
	for i, x := range t.list {
		if x.ID == cur {
			t.cursor = i
		}
	}
	t.cursor = min(max(t.cursor, 0), max(0, len(t.list)-1))
}

func (m *Model) selectedTask() *task.Task {
	t := &m.tasks
	if t.cursor >= 0 && t.cursor < len(t.list) {
		return t.list[t.cursor]
	}
	return nil
}

// lastRun is task id's newest run.
func (m *Model) lastRun(id string) *task.Run {
	if m.tasks.st == nil {
		return nil
	}
	runs := m.tasks.st.RunsOf(id)
	if len(runs) == 0 {
		return nil
	}
	return runs[len(runs)-1]
}

func (m *Model) selectedRun() *task.Run {
	if x := m.selectedTask(); x != nil {
		return m.lastRun(x.ID)
	}
	return nil
}

// needsYou: x is open and its newest run waits for an answer, asked something, went quiet, failed or is unknown.
func (m *Model) needsYou(x *task.Task) bool {
	r := m.lastRun(x.ID)
	return x.Status == task.StatusTodo && r != nil && r.NeedsYou()
}

func (m *Model) openTaskCount() int {
	n := 0
	if m.tasks.st != nil {
		for _, x := range m.tasks.st.Tasks {
			if x.Status == task.StatusTodo || m.tasks.st.Running(x.ID) {
				n++
			}
		}
	}
	return n
}

// taskKey: the list keys in the Tasks view; ok is false for keys the view leaves to the common handling.
func (m *Model) taskKey(a act) (tea.Cmd, bool) {
	t := &m.tasks
	move := func(d int) {
		t.cursor = min(max(t.cursor+d, 0), max(0, len(t.list)-1))
	}
	page := max(1, m.listHeight()/2)
	switch a {
	case actDown:
		move(1)
	case actUp:
		move(-1)
	case actPageDown:
		move(page)
	case actPageUp:
		move(-page)
	case actHalfDown:
		move(page / 2)
	case actHalfUp:
		move(-page / 2)
	case actTop:
		t.cursor = 0
	case actBottom:
		move(len(t.list))
	case actSort:
		m.cycleLayout()
	case actLeft, actRight:
		if t.layout == layoutBoard {
			m.boardMove(map[act]int{actLeft: -1, actRight: 1}[a])
		}
	case actExtendDown, actExtendUp:
		m.extendMarks(map[act]int{actExtendDown: 1, actExtendUp: -1}[a])
	case actEnter, actResume:
		if len(t.marked) > 1 {
			m.openRunDialog()
			return nil, true
		}
		m.openTask()
	case actSpace:
		m.viewRunSession()
	case actNew:
		return m.openTaskForm(nil), true
	case actEdit:
		return m.editTask(), true
	case actDone:
		return m.toggleTaskDone(), true
	case actPause:
		return m.toggleTaskPause(), true
	case actCloseTab:
		m.askStopRun()
	case actFoldAll:
		m.setEditsOpen(nil)
	case actFold, actUnfold:
		open := a == actUnfold
		m.setEditsOpen(&open)
	case actBack:
		if len(t.marked) > 0 {
			t.marked, t.anchor = nil, ""
			return nil, true
		}
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.filterTasks()
		}
	case actQuit, actHelp, actSettings, actNextView, actPrevView, actView, actSearch, actPalette, actUndo:
		return nil, false
	default:
		if b := bindingOf(inList, a); b != nil && b.tier != tierNav {
			m.flash(i18n.T("tasks.not_here"))
		}
	}
	return nil, true
}

func (m *Model) toggleTaskDone() tea.Cmd {
	if len(m.tasks.marked) > 1 {
		return m.toggleMarkedDone()
	}
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	id, cmd := x.ID, commandID()
	status, note := task.StatusDone, i18n.F("tasks.done", render.Truncate(render.OneLine(x.Title), 40))
	if x.Status != task.StatusTodo {
		status, note = task.StatusTodo, i18n.F("tasks.reopened", render.Truncate(render.OneLine(x.Title), 40))
	}
	return m.writeAs(cmd, coord.MTaskStatus, task.TaskStatus{ID: id, Status: status}, "", func(mm *Model) tea.Cmd {
		mm.offerUndo(note, func(mm *Model) tea.Cmd {
			return mm.write(coord.MTaskUndo, coord.TaskUndo{ID: id, Command: cmd}, i18n.F("undo.done", render.Truncate(render.OneLine(x.Title), 40)), nil)
		})
		return nil
	})
}

// toggleTaskPause pauses dispatch under the selected task tree, or resumes it: what runs there finishes either way.
func (m *Model) toggleTaskPause() tea.Cmd {
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	switch {
	case task.Finished(x.Status):
		m.flash(i18n.T("tasks.pause_finished"))
		return nil
	case x.Paused == nil && !m.tasks.st.Tree(x):
		m.flash(i18n.T("tasks.pause_no_tree"))
		return nil
	}
	on, note := x.Paused == nil, i18n.F("tasks.resumed", render.Truncate(x.Title, 40))
	if on {
		note = i18n.F("tasks.paused", render.Truncate(x.Title, 40))
	}
	return m.write(coord.MTaskPause, task.TaskPause{ID: x.ID, On: on}, note, nil)
}

func (m *Model) askStopRun() {
	r := m.selectedRun()
	if r == nil || !task.Open(r.State) || r.Want == "stop" {
		m.flash(i18n.T("tasks.nothing_running"))
		return
	}
	id := r.ID
	m.openConfirm(i18n.F("tasks.stop_title", id), i18n.T("tasks.btn_stop"), []string{i18n.F("tasks.stop_body", r.Machine, r.Agent)},
		func(mm *Model) {
			mm.pending = mm.write(coord.MRunStop, task.RunRef{ID: id}, i18n.F("tasks.stopping", id), nil)
		}, nil)
}

// sessionRec is the record of r's session: the list's own when it has one, else a bare one to resume from.
func (m *Model) sessionRec(r *task.Run) *tend.Rec {
	if r == nil || r.Session == "" {
		return nil
	}
	if r.Machine == coord.Local {
		if have := m.bySession(r.Session); have != nil {
			return have
		}
	} else if hr := m.remote[r.Machine]; hr != nil {
		for _, x := range hr.recs {
			if x.SessionID == r.Session {
				return x
			}
		}
	}
	rec := &tend.Rec{Provider: r.Provider, SessionID: r.Session, Title: r.Title, Cwd: r.Dir, Project: projectName(r.Dir)}
	if r.Machine != coord.Local {
		rec.Host = r.Machine
	}
	return rec
}

func projectName(dir string) string {
	dir = strings.TrimRight(strings.ReplaceAll(dir, `\`, "/"), "/")
	return dir[strings.LastIndex(dir, "/")+1:]
}

// viewRunSession shows the conversation of the selected task's last run in the Sessions view.
func (m *Model) viewRunSession() {
	r := m.selectedRun()
	rec := m.sessionRec(r)
	if rec == nil {
		m.flash(i18n.T("tasks.no_session"))
		return
	}
	m.closeOverlay()
	m.Focus(rec)
}

// takeOver resumes the last run's session here: the resume dialog, which warns while the run still drives it.
func (m *Model) takeOver() {
	rec := m.sessionRec(m.selectedRun())
	if rec == nil {
		m.flash(i18n.T("tasks.no_session"))
		return
	}
	m.closeOverlay()
	m.openResume(rec)
}

// openTask: the task dialog — run it, stop or abandon its run, take its session over, edit, mark done.
func (m *Model) openTask() {
	if m.selectedTask() == nil {
		return
	}
	m.ov = overlay{kind: ovTask, focus: -1}
}

func (m *Model) taskButtons() []btn {
	x, r := m.selectedTask(), m.selectedRun()
	if x == nil {
		return nil
	}
	var bs []btn
	open := r != nil && task.Open(r.State)
	waiting := r != nil && r.Waiting()
	asks := waitsOn(r) != nil
	if waiting {
		bs = append(bs, btn{keyed(enterKey, i18n.T("tasks.btn_reply")), true, (*Model).openReply})
	}
	if asks {
		bs = append(bs, btn{keyed(enterKey, i18n.T("tasks.btn_answer")), true, (*Model).openAnswer})
	}
	if canSend(r) {
		bs = append(bs, btn{i18n.T("tasks.btn_send"), false, (*Model).openSend})
	}
	draft := hasDraft(x)
	if draft {
		bs = append(bs, btn{keyed(enterKey, i18n.F("tasks.btn_draft", len(x.Draft.Plan.Tasks))), !waiting && !asks, (*Model).openDraft})
	}
	if !open && x.Status == task.StatusTodo {
		label, primary := i18n.T("tasks.btn_run"), !waiting && !draft
		if primary {
			label = keyed(enterKey, label)
		}
		bs = append(bs, btn{label, primary, (*Model).openRunDialog})
		if !draft && len(m.tasks.st.Children(x.ID)) == 0 {
			bs = append(bs, btn{i18n.T("tasks.btn_plan"), false, func(mm *Model) { mm.closeOverlay(); mm.pending = mm.planTask() }})
		}
	}
	if canReply(r) && !waiting {
		bs = append(bs, btn{i18n.T("tasks.btn_reply"), false, (*Model).openReply})
	}
	if open && r.Want != "stop" {
		bs = append(bs, btn{keyed(keyOf(inList, actCloseTab), i18n.T("tasks.btn_stop")), false, func(mm *Model) { mm.closeOverlay(); mm.askStopRun() }})
	}
	if r != nil && (r.State == task.Starting || r.State == task.Running || r.State == task.Unknown) {
		id := r.ID
		bs = append(bs, btn{i18n.T("tasks.btn_abandon"), false, func(mm *Model) {
			mm.closeOverlay()
			mm.pending = mm.write(coord.MRunAbandon, task.RunRef{ID: id}, i18n.F("tasks.abandoned", id), nil)
		}})
	}
	if r != nil && m.tasks.watch == r.ID {
		bs = append(bs, btn{i18n.T("tasks.btn_unwatch"), false, func(mm *Model) { mm.tasks.watch = ""; mm.closeOverlay() }})
	} else if r != nil {
		id := r.ID
		bs = append(bs, btn{i18n.T("tasks.btn_watch"), false, func(mm *Model) {
			mm.tasks.watch = id
			mm.closeOverlay()
			mm.flash(i18n.F("tasks.watch_on", id))
		}})
	}
	if r != nil && r.Session != "" {
		bs = append(bs, btn{i18n.T("tasks.btn_take_over"), !open && x.Status != task.StatusTodo, (*Model).takeOver})
		bs = append(bs, btn{keyed(keyName("space"), i18n.T("tasks.btn_view_session")), false, (*Model).viewRunSession})
	}
	if !task.Finished(x.Status) && (x.Paused != nil || m.tasks.st.Tree(x)) {
		label := i18n.T("tasks.btn_pause")
		if x.Paused != nil {
			label = i18n.T("tasks.btn_resume")
		}
		bs = append(bs, btn{keyed(keyOf(inList, actPause), label), false, func(mm *Model) { mm.closeOverlay(); mm.pending = mm.toggleTaskPause() }})
	}
	bs = append(bs, btn{keyed(keyOf(inList, actEdit), i18n.T("key.edit")), false, func(mm *Model) {
		mm.closeOverlay()
		mm.pending = mm.editTask()
	}})
	if m.taskProject() != nil {
		bs = append(bs, btn{i18n.T("tasks.btn_project"), false, func(mm *Model) { mm.pending = mm.editProject() }})
	}
	done := i18n.T("key.done")
	if x.Status != task.StatusTodo {
		done = i18n.T("tasks.btn_reopen")
	}
	bs = append(bs, btn{keyed(keyOf(inList, actDone), done), false, func(mm *Model) { mm.closeOverlay(); mm.pending = mm.toggleTaskDone() }})
	return append(bs, cancelBtn())
}

func (m *Model) renderTask() string {
	w := m.ovWidth()
	inner := w - 4
	x := m.selectedTask()
	if x == nil {
		return ovRender([]string{dimmed.Render(i18n.T("tasks.gone"))}, w)
	}
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(render.OneLine(x.Title), inner))}
	body = append(body, dimmed.Render(render.Truncate(m.taskWhere(x), inner)))
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	if r := m.selectedRun(); r != nil {
		body = append(body, m.runLine(r, inner, false))
		body = append(body, runFacts(r, inner, 4, m.now)...)
	} else {
		body = append(body, dimmed.Render(i18n.T("tasks.never_ran")))
	}
	body = append(body, "")
	body = append(body, m.buttons(len(body)+1, m.taskButtons())...)
	return ovRender(body, w)
}

func (m *Model) taskDialogKey(msg tea.KeyPressMsg) tea.Cmd {
	switch a := keyAct(inList, msg.String()); a {
	case actCloseTab:
		m.closeOverlay()
		m.askStopRun()
		return nil
	case actEdit:
		m.closeOverlay()
		return m.editTask()
	case actDone:
		m.closeOverlay()
		return m.toggleTaskDone()
	case actPause:
		m.closeOverlay()
		return m.toggleTaskPause()
	case actSpace:
		m.viewRunSession()
		return nil
	}
	m.dialogKey(keyAct(inConfirm, msg.String()), func() {
		if bs := m.taskButtons(); len(bs) > 0 {
			for _, b := range bs {
				if b.primary {
					b.act(m)
					return
				}
			}
		}
	})
	return nil
}

var taskStatusKeys = map[string]string{
	task.StatusTodo: "tasks.status_todo", task.StatusDone: "tasks.status_done", task.StatusCanceled: "tasks.status_canceled",
}

var runStateKeys = map[string]string{
	task.Queued: "tasks.run_queued", task.Starting: "tasks.run_starting", task.Running: "tasks.run_running",
	task.Unknown: "tasks.run_unknown", task.Exited: "tasks.run_exited", task.Stopped: "tasks.run_stopped",
	task.Failed: "tasks.run_failed", task.Canceled: "tasks.run_canceled", task.Abandoned: "tasks.run_abandoned",
}

// runFacts: why r ended, what it asked, wants or last noted, when it last put anything out, and what to do next, at
// most room lines of question.
func runFacts(r *task.Run, inner, room int, now time.Time) []string {
	var out []string
	if r.Reason != "" && !task.Open(r.State) {
		why := render.OneLine(render.RunReason(r.Reason))
		if r.Detail != "" && r.Detail != r.Reason {
			why += " — " + render.OneLine(r.Detail)
		}
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.reason", why), inner)))
	}
	if r.Ask != "" && r.Attention == task.AttentionPermission && len(r.Requests) == 0 {
		out = append(out, accent.Render(render.Truncate(i18n.F("tasks.wants", render.OneLine(r.Ask)), inner)))
	} else if r.Ask != "" && !asked(r) {
		lines := render.Wrap(i18n.F("tasks.ask", render.Sanitize(r.Ask)), inner)
		if len(lines) > room {
			lines = append(lines[:room-1], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-room+1)))
		}
		for _, l := range lines {
			out = append(out, accent.Render(l))
		}
	} else if r.Note != "" && task.Open(r.State) {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.note", render.OneLine(r.Note)), inner)))
	} else if r.Last != "" && task.Open(r.State) {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.last", render.OneLine(r.Last)), inner)))
	}
	if r.OutputAt != nil && task.Open(r.State) {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.output_at", render.Elapsed(*r.OutputAt, now)), inner)))
	}
	if u := render.RunUsage(r.Usage); u != "" {
		out = append(out, dimmed.Render(render.Truncate(u, inner)))
	}
	out = append(out, requestLines(r, inner)...)
	if h := render.RunHint(r); h != "" && !r.Waiting() {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.next", h), inner)))
	}
	return out
}

// taskWhere: status, default machine and agent, directory.
func (m *Model) taskWhere(x *task.Task) string {
	parts := []string{i18n.T(taskStatusKeys[x.Status])}
	if m.tasks.st != nil && m.tasks.st.Running(x.ID) {
		parts[0] = i18n.T("tasks.status_running")
	}
	for _, s := range []string{x.Machine, x.Agent} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if x.Dir != "" {
		parts = append(parts, paths.Tilde(x.Dir))
	}
	return render.OneLine(strings.Join(parts, " · "))
}

// runLine: one run — state, machine, agent, how long.
func (m *Model) runLine(r *task.Run, w int, dim bool) string {
	glyph, sty := runGlyph(r)
	took := "-"
	if r.StartedAt != nil {
		end := m.now
		if r.EndedAt != nil {
			end = *r.EndedAt
		}
		took = render.Elapsed(*r.StartedAt, end)
	}
	text := render.OneLine(i18n.F("tasks.run_line", runStateText(r), r.Machine, r.Agent, took))
	if dim {
		sty = dimmed
	}
	return sty.Render(glyph+" ") + render.Truncate(text, max(1, w-3))
}

// waitGlyph is how a run that wants someone looks, by what it wants: a permission, a question (or a wait nothing
// names), or no output for long; ok is false when it wants nobody.
func waitGlyph(r *task.Run) (glyph string, sty lipgloss.Style, ok bool) {
	switch {
	case r.Attention == task.AttentionPermission:
		return render.GlyphWarn, warnSty, true
	case r.Attention == task.AttentionAsked:
		return render.GlyphAsk, accent, true
	case r.Attention == task.AttentionStalled && task.Open(r.State):
		return render.GlyphStall, errSty, true
	}
	return "", lipgloss.Style{}, false
}

func runGlyph(r *task.Run) (string, lipgloss.Style) {
	switch r.State {
	case task.Running, task.Starting:
		if g, sty, ok := waitGlyph(r); ok {
			return g, sty
		}
		return render.GlyphLive, accent
	case task.Queued:
		return render.GlyphClock, dimmed
	case task.Exited:
		if r.ExitCode != nil && *r.ExitCode != 0 {
			return render.GlyphErr, errSty
		}
		return render.GlyphDone, okSty
	case task.Failed, task.Unknown:
		return render.GlyphWarn, errSty
	}
	return render.GlyphArchive, dimmed
}

func runStateText(r *task.Run) string {
	s := i18n.T(runStateKeys[r.State])
	if r.State == task.Exited && r.ExitCode != nil && *r.ExitCode != 0 {
		s = i18n.F("tasks.exit_code", s, *r.ExitCode)
	}
	if r.Want == "stop" && task.Open(r.State) {
		s = i18n.F("tasks.stopping_state", s)
	}
	if a := render.RunAttention(r); a != "" {
		s = i18n.F("tasks.with_attention", s, a)
	}
	return s
}

func taskGlyph(m *Model, x *task.Task) (string, lipgloss.Style) {
	switch {
	case m.needsYou(x):
		if g, sty, ok := waitGlyph(m.lastRun(x.ID)); ok {
			return g, sty
		}
		return render.GlyphWarn, errSty
	case m.tasks.st != nil && m.tasks.st.Running(x.ID):
		return render.GlyphLive, accent
	case x.Status == task.StatusDone:
		return render.GlyphDone, okSty
	case x.Status == task.StatusCanceled:
		return render.GlyphArchive, dimmed
	}
	return render.GlyphSession, dimmed
}

// tasksBody: the task list and, when wide enough, the selected task's details.
func (m *Model) tasksBody(y0, h int) []string {
	if m.w < compactCols || !m.twoColumn() || m.tasks.layout == layoutBoard {
		return m.taskList(y0, 0, m.w, h)
	}
	listW := m.listWidth()
	prevW := m.w - listW - 1
	left := m.taskList(y0, 0, listW, h)
	right := m.taskDetail(listW+1, y0, prevW, h)
	out := make([]string, h)
	for i := range out {
		out[i] = fit(at(left, i), listW) + " " + fit(at(right, i), prevW)
	}
	return out
}

func (m *Model) taskList(y0, x0, w, h int) []string {
	t := &m.tasks
	title := i18n.F("tasks.title", m.openTaskCount(), t.matched)
	if t.st != nil {
		if n := len(t.st.NeedsYou()); n > 0 {
			title = i18n.F("tasks.title_needs_you", m.openTaskCount(), t.matched, n)
		}
	}
	title += " · " + i18n.T(layoutKeys[t.layout])
	out := []string{fit(dimmed.Render(title), w)}
	switch {
	case t.connect == nil:
		return append(out, dimmed.Render(i18n.T("tasks.unavailable")))
	case t.err != nil && !t.loaded:
		out = append(out, errSty.Render(render.Truncate(i18n.F("tasks.no_coordinator", reasonText(t.err)), w)))
		return append(out, dimmed.Render(render.Truncate(i18n.T("tasks.no_coordinator_hint"), w)))
	case !t.loaded:
		return append(out, dimmed.Render(i18n.T("tasks.loading")))
	case len(t.st.Tasks) == 0 || len(t.list) == 0 && t.layout != layoutHome:
		return append(out, dimmed.Render(render.Truncate(i18n.F("tasks.empty", keyOf(inList, actNew)), w)))
	}
	if t.layout == layoutBoard {
		return m.board(out, y0, x0, w, h)
	}
	return m.taskRows(out, y0, x0, w, h)
}

// taskDetail is drawn with its top left corner at x0, y0.
func (m *Model) taskDetail(x0, y0, w, h int) []string {
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	if r := m.watchedRun(); r != nil && h >= 16 && (m.selectedRun() == nil || m.selectedRun().ID != r.ID) {
		below := h / 2
		return append(m.taskDetailIn(x, x0, y0, w, h-below), m.watchPanel(r, x0, y0+h-below, w, below)...)
	}
	return m.taskDetailIn(x, x0, y0, w, h)
}

// watchPanel follows the watched run: its task, how it stands and its output.
func (m *Model) watchPanel(r *task.Run, x0, y0, w, h int) []string {
	inner := w - 4
	title := r.Task
	if x := m.tasks.st.Tasks[r.Task]; x != nil {
		title = x.Title
	}
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(render.OneLine(title), inner)), m.runLine(r, inner, false)}
	if room := h - 2 - len(body); room > 0 {
		body = append(body, m.outputLines(r, x0+2, y0+1+len(body), inner, room)...)
	}
	return panel(i18n.F("tasks.watching", r.ID), body, w, h)
}

func (m *Model) watchedRun() *task.Run {
	if m.tasks.watch == "" || m.tasks.st == nil {
		return nil
	}
	return m.tasks.st.Runs[m.tasks.watch]
}

func (m *Model) taskDetailIn(x *task.Task, x0, y0, w, h int) []string {
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(render.OneLine(x.Title), inner)))
	body = append(body, dimmed.Render(render.Truncate(m.taskWhere(x), inner)))
	if brief := strings.TrimSpace(x.Brief); brief != "" && brief != x.Title {
		body = append(body, "")
		lines := render.Wrap(render.Sanitize(brief), inner)
		if len(lines) > 6 {
			lines = append(lines[:5], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-5)))
		}
		body = append(body, lines...)
	}
	if hasDraft(x) {
		body = append(body, "", accent.Render(render.Truncate(i18n.F("tasks.draft_line", len(x.Draft.Plan.Tasks), len(x.Draft.Plan.Questions)), inner)))
	}
	if p := m.tasks.st.PausedBy(x.ID); p != nil && !task.Finished(x.Status) {
		line := i18n.T("tasks.paused_line")
		if p.ID != x.ID {
			line = i18n.F("tasks.paused_under", render.Sanitize(p.Title))
		}
		body = append(body, "", warnSty.Render(render.Truncate(line, inner)))
	}
	if a := m.tasks.fold.Aff.Tasks[x.ID]; a != nil && a.TreeDone != nil {
		body = append(body, "", okSty.Render(render.Truncate(render.GlyphDone+" "+treeDoneLine(*a.TreeDone), inner)))
	}
	runs := m.tasks.st.RunsOf(x.ID)
	if len(runs) > 0 {
		body = append(body, "", accent.Render(i18n.F("tasks.runs", len(runs))))
		for i := len(runs) - 1; i >= 0 && i >= len(runs)-4; i-- {
			body = append(body, m.runLine(runs[i], inner, i != len(runs)-1))
			if i == len(runs)-1 {
				body = append(body, runFacts(runs[i], inner, 3, m.now)...)
			}
		}
	}
	if r := m.selectedRun(); r != nil {
		room := h - 2 - len(body) - 2
		if room > 2 {
			body = append(body, "", accent.Render(i18n.F("tasks.output", r.ID)))
			body = append(body, m.outputLines(r, x0+2, y0+1+len(body), inner, room)...)
		}
	}
	return panel(i18n.T("tasks.detail"), body, w, h)
}

// outputLines are the last room lines of r's output as render.RunOutputLines reads its events, wrapped to inner and
// drawn from x0, y0: an edit row with a first hunk opens or closes on a click.
func (m *Model) outputLines(r *task.Run, x0, y0, inner, room int) []string {
	t := &m.tasks
	o := t.out[r.ID]
	ok := o != nil && o.loaded
	var events []output.Event
	if o != nil {
		events = o.events
	}
	open := func(key string) bool { return t.editsOpen != t.openEdits[r.ID+" "+key] }
	var lines, keys []string
	for _, l := range render.RunOutputLines(events, inner, open) {
		text := render.Sanitize(l.Text())
		switch {
		case len(l.Spans) > 1 || l.Spans[0].Tone != "":
			lines, keys = append(lines, toned(l)), append(keys, l.Edit)
		case render.Width(text) <= inner:
			lines, keys = append(lines, text), append(keys, l.Edit)
		default:
			for _, w := range render.Wrap(text, inner) {
				lines, keys = append(lines, w), append(keys, "")
			}
		}
	}
	switch {
	case !ok:
		lines, keys = []string{dimmed.Render(i18n.T("tasks.loading"))}, nil
	case len(lines) == 0:
		lines, keys = []string{dimmed.Render(i18n.T("tasks.no_output"))}, nil
	}
	if len(lines) > room {
		lines = lines[len(lines)-room:]
		if keys != nil {
			keys = keys[len(keys)-room:]
		}
	}
	for i, key := range keys {
		if key != "" {
			k := r.ID + " " + key
			m.mark(y0+i, x0, inner, func(m *Model) {
				if m.tasks.openEdits == nil {
					m.tasks.openEdits = map[string]bool{}
				}
				m.tasks.openEdits[k] = !m.tasks.openEdits[k]
			})
		}
	}
	return lines
}

var toneSty = map[string]*lipgloss.Style{render.ToneAdd: &okSty, render.ToneDel: &errSty, render.ToneHunk: &accent, render.ToneDim: &dimmed}

// toned is l with each span in its tone's colour.
func toned(l render.OutputLine) string {
	var b strings.Builder
	for _, sp := range l.Spans {
		text := render.Sanitize(sp.Text)
		if sty := toneSty[sp.Tone]; sty != nil {
			text = sty.Render(text)
		}
		b.WriteString(text)
	}
	return b.String()
}

// setEditsOpen opens (or closes) every edit row of the output shown; nil turns them the other way.
func (m *Model) setEditsOpen(open *bool) {
	t := &m.tasks
	if open == nil {
		t.editsOpen = !t.editsOpen
	} else {
		t.editsOpen = *open
	}
	t.openEdits = nil
}

// tasksStatus replaces the filter chips in the Tasks view: who coordinates and how the machines stand.
func (m *Model) tasksStatus() string {
	t := &m.tasks
	var parts []string
	switch {
	case t.cl != nil && t.cl.Coord != nil:
		parts = append(parts, i18n.T("tasks.coord_here"))
	case t.cl != nil:
		parts = append(parts, i18n.T("tasks.coord_service"))
	case t.connecting:
		parts = append(parts, i18n.T("tasks.connecting"))
	}
	for _, mc := range t.machines {
		s := mc.Name
		switch mc.State {
		case coord.MachineConnected:
			s = okSty.Render(render.GlyphOK) + " " + s
		case coord.MachineOffline:
			s = errSty.Render(render.GlyphErr) + " " + s
		default:
			s = dimmed.Render("-") + " " + s
		}
		if mc.Active+mc.Queued > 0 {
			s += dimmed.Render(i18n.F("tasks.machine_load", mc.Active, mc.Slots, mc.Queued))
		}
		parts = append(parts, s)
	}
	return " " + strings.Join(parts, dimmed.Render("   "))
}

// remoteCoordinator: mode 2, the coordinator is a server; this machine is no node.
func (m *Model) remoteCoordinator() bool {
	return m.cfg.Coordinator != nil && m.cfg.Coordinator.URL != ""
}

func closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (m *Model) machineNames() []string {
	var out []string
	for _, mc := range m.tasks.machines {
		out = append(out, mc.Name)
	}
	if len(out) == 0 && !m.remoteCoordinator() {
		out = []string{coord.Local}
	}
	return out
}

func (m *Model) agentNames() []string {
	var out []string
	for _, a := range m.tasks.agents {
		out = append(out, a.Name)
	}
	if len(out) == 0 {
		out = []string{tend.ProviderClaude, tend.ProviderCodex}
	}
	return out
}

package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// Connector reaches the coordinator: the running one, or this process becoming it.
type Connector func(wire.Options) (*coord.Client, error)

// tasksState is the Tasks view: the coordinator's state, polled while the view shows.
type tasksState struct {
	connect    Connector
	cl         *coord.Client
	connecting bool
	err        error // connecting failed; retried when the view is opened again
	st         *task.State
	loaded     bool
	machines   []coord.Machine
	agents     []tend.AgentProfile
	list       []*task.Task // shown, filtered by the search box
	cursor     int
	scroll     int
	out        map[string]runOutput // by run id
	ticking    bool
	polling    bool
	feed       *feed     // journal pushes of the current connection
	subscribed bool      // the state follows the pushes; a full read is needed only after a gap
	machinesAt time.Time // when the machines were last read
}

// feed carries the coordinator's journal pushes from the connection to the model; lost is set when it overflowed.
type feed struct {
	ch   chan journal.Envelope
	lost atomic.Bool
}

func newFeed() *feed { return &feed{ch: make(chan journal.Envelope, 512)} }

func (f *feed) options() wire.Options {
	return wire.Options{OnPush: func(method string, params json.RawMessage) {
		var env journal.Envelope
		if method != coord.PushJournal || json.Unmarshal(params, &env) != nil {
			return
		}
		select {
		case f.ch <- env:
		default:
			f.lost.Store(true)
		}
	}}
}

type runOutput struct {
	text string
	end  bool // the run had ended when this was read: no need to read it again
}

const (
	tasksEvery    = 2 * time.Second
	machinesEvery = 5 * time.Second
	tasksWait     = 20 * time.Second
	tailBytes     = 16 << 10
)

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

type tasksStateMsg struct {
	st       *task.State
	machines []coord.Machine
	agents   []tend.AgentProfile
	err      error
}

type tasksTickMsg struct{}

type runOutMsg struct {
	run  string
	text string
	end  bool
	err  error
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

// tasksOpen starts connecting and polling when the Tasks view shows.
func (m *Model) tasksOpen() tea.Cmd {
	t := &m.tasks
	switch {
	case t.connect == nil:
		return nil
	case t.cl == nil && !t.connecting:
		t.connecting, t.err = true, nil
		connect, f := t.connect, newFeed()
		t.feed, t.subscribed = f, false
		return func() tea.Msg {
			cl, err := connect(f.options())
			return tasksConnMsg{cl, err}
		}
	case t.cl != nil && !t.ticking:
		t.ticking = true
		return tea.Batch(m.pollTasks(), tickTasks())
	}
	return nil
}

// pollTasks reads what the pushes do not bring: the whole state when it is not followed yet or a push was lost, the
// machines every machinesEvery, and the selected run's output while it runs.
func (m *Model) pollTasks() tea.Cmd {
	t := &m.tasks
	if t.cl == nil || t.polling {
		return nil
	}
	var cmds []tea.Cmd
	if r := m.selectedRun(); r != nil {
		if o, ok := t.out[r.ID]; !ok || !o.end {
			cmds = append(cmds, readOutput(t.cl, r.ID, !task.Open(r.State)))
		}
	}
	if t.subscribed && !t.feed.lost.Load() && t.st != nil {
		if time.Since(t.machinesAt) < machinesEvery {
			return tea.Batch(cmds...)
		}
		t.machinesAt = time.Now()
		cl := t.cl
		return tea.Batch(append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
			defer cancel()
			var ms coord.Machines
			err := cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms)
			return tasksMachinesMsg{machines: ms.Machines, err: err}
		})...)
	}
	t.polling = true
	t.feed.lost.Store(false)
	t.machinesAt = time.Now()
	cl, needAgents := t.cl, len(t.agents) == 0
	cmds = append(cmds, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var msg tasksStateMsg
		st := task.New()
		msg.err = cl.Call(ctx, coord.MStateGet, nil, st)
		if wire.Code(msg.err) == wire.CodeInternal { // too big for one frame: without the briefs (search misses them)
			st = task.New()
			msg.err = cl.Call(ctx, coord.MStateGet, coord.StateParams{NoBriefs: true}, st)
		}
		if msg.err != nil {
			return msg
		}
		msg.st = st
		var ms coord.Machines
		if cl.Call(ctx, coord.MMachineList, coord.MachinesParams{}, &ms) == nil {
			msg.machines = ms.Machines
		}
		if needAgents {
			var as coord.Agents
			if cl.Call(ctx, coord.MAgentList, nil, &as) == nil {
				msg.agents = as.Agents
			}
		}
		return msg
	})
	return tea.Batch(cmds...)
}

func tickTasks() tea.Cmd {
	return tea.Tick(tasksEvery, func(time.Time) tea.Msg { return tasksTickMsg{} })
}

type tasksMachinesMsg struct {
	machines []coord.Machine
	err      error
}

func (msg tasksMachinesMsg) apply(m *Model) tea.Cmd {
	if msg.err == nil {
		m.tasks.machines = msg.machines
	}
	return nil
}

// subscribe asks for the journal after seq on cl; the pushes then arrive on f.
func subscribe(cl *coord.Client, f *feed, seq int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		err := cl.Call(ctx, coord.MSubscribe, coord.SubscribeParams{AfterSeq: seq}, nil)
		return tasksSubscribedMsg{cl: cl, f: f, err: err}
	}
}

type tasksSubscribedMsg struct {
	cl  *coord.Client
	f   *feed
	err error
}

func (msg tasksSubscribedMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	if t.cl != msg.cl || t.feed != msg.f {
		return nil
	}
	if msg.err != nil {
		t.subscribed = false
		return nil
	}
	return waitPush(msg.cl, msg.f)
}

// waitPush waits for the next journal pushes (all that are there) or the end of the connection.
func waitPush(cl *coord.Client, f *feed) tea.Cmd {
	return func() tea.Msg {
		select {
		case env := <-f.ch:
			envs := []journal.Envelope{env}
			for len(envs) < cap(f.ch) {
				select {
				case env := <-f.ch:
					envs = append(envs, env)
					continue
				default:
				}
				break
			}
			return tasksPushMsg{cl: cl, f: f, envs: envs}
		case <-cl.Done():
			return tasksPushMsg{cl: cl, f: f, closed: true}
		}
	}
}

type tasksPushMsg struct {
	cl     *coord.Client
	f      *feed
	envs   []journal.Envelope
	closed bool
}

// apply folds the pushed envelopes into the state; a gap in seq makes the next poll read the whole state.
func (msg tasksPushMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	if t.cl != msg.cl || t.feed != msg.f {
		return nil
	}
	if msg.closed {
		t.subscribed = false
		return nil
	}
	for _, env := range msg.envs {
		switch {
		case t.st == nil || env.Seq <= t.st.Seq:
		case env.Seq == t.st.Seq+1 && t.st.Apply(env) == nil:
		default:
			msg.f.lost.Store(true)
		}
	}
	m.filterTasks()
	cmd := waitPush(msg.cl, msg.f)
	if msg.f.lost.Load() && m.view == viewTasks && !t.polling {
		cmd = tea.Batch(cmd, m.pollTasks())
	}
	return cmd
}

func readOutput(cl *coord.Client, run string, ended bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var tail node.Tail
		err := cl.Call(ctx, coord.MRunTail, coord.TailParams{Run: run, Before: -1, Max: tailBytes}, &tail)
		return runOutMsg{run: run, text: render.Sanitize(tail.Text), end: ended, err: err}
	}
}

func (msg tasksConnMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	t.connecting = false
	if msg.err != nil {
		t.err = msg.err
		return nil
	}
	t.cl = msg.cl
	if m.view != viewTasks {
		return nil
	}
	t.ticking = true
	return tea.Batch(m.pollTasks(), tickTasks())
}

func (msg tasksStateMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	t.polling = false
	if msg.err != nil {
		t.err = msg.err
		if t.cl != nil && closed(t.cl.Done()) { // the connection ended (the coordinator went away, keepalive): connect again
			t.cl.Close()
			t.cl = nil
		}
	} else {
		t.err, t.st, t.loaded = nil, msg.st, true
		t.machines = msg.machines
		if len(msg.agents) > 0 {
			t.agents = msg.agents
		}
		m.filterTasks()
	}
	if msg.err == nil && !t.subscribed && t.cl != nil && t.feed != nil {
		t.subscribed = true // the pushes after msg.st.Seq are replayed, so the state is followed from here
		return subscribe(t.cl, t.feed, msg.st.Seq)
	}
	return nil
}

// apply is the one place the next tick is set, so one loop runs while the view shows.
func (tasksTickMsg) apply(m *Model) tea.Cmd {
	if m.view != viewTasks {
		m.tasks.ticking = false
		return nil
	}
	if m.tasks.cl == nil {
		m.tasks.ticking = false
		return m.tasksOpen()
	}
	return tea.Batch(m.pollTasks(), tickTasks())
}

func (msg runOutMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		return nil
	}
	if m.tasks.out == nil {
		m.tasks.out = map[string]runOutput{}
	}
	m.tasks.out[msg.run] = runOutput{text: msg.text, end: msg.end}
	return nil
}

func (msg taskDoneMsg) apply(m *Model) tea.Cmd {
	if msg.err != nil {
		m.flash(i18n.F("tasks.failed", reasonText(msg.err)))
		return nil
	}
	if msg.note != "" {
		m.flash(msg.note)
	}
	cmd := m.pollTasks()
	if msg.then != nil {
		cmd = tea.Batch(cmd, msg.then(m))
	}
	return cmd
}

// reasonText: a protocol error as a short localized phrase with its detail.
func reasonText(err error) string {
	var detail string
	if e, ok := err.(*wire.Error); ok {
		detail = e.Detail
	}
	r := remote.Reason(err)
	if detail != "" {
		return r + " — " + detail
	}
	return r
}

// write sends a command with a fresh id; note is flashed when it succeeds.
func (m *Model) write(method string, params any, note string, then func(*Model) tea.Cmd) tea.Cmd {
	cl := m.tasks.cl
	if cl == nil {
		m.flash(i18n.T("tasks.unavailable"))
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tasksWait)
		defer cancel()
		var b [8]byte
		rand.Read(b[:])
		err := cl.CallCommand(ctx, method, "tui-"+hex.EncodeToString(b[:]), params, nil)
		return taskDoneMsg{note: note, err: err, then: then}
	}
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
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	match := func(x *task.Task) bool {
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
	t.list = append(append(needs, open...), closed...)
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
	case actEnter, actResume:
		m.openTask()
	case actSpace:
		m.viewRunSession()
	case actNew:
		return m.openTaskForm(nil), true
	case actEdit:
		return m.editTask(), true
	case actDone:
		return m.toggleTaskDone(), true
	case actCloseTab:
		m.askStopRun()
	case actBack:
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.filterTasks()
		}
	case actQuit, actHelp, actSettings, actNextView, actPrevView, actView, actSearch:
		return nil, false
	default:
		if b := bindingOf(inList, a); b != nil && b.tier != tierNav {
			m.flash(i18n.T("tasks.not_here"))
		}
	}
	return nil, true
}

func (m *Model) toggleTaskDone() tea.Cmd {
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	status, note := task.StatusDone, i18n.F("tasks.done", render.Truncate(x.Title, 40))
	if x.Status != task.StatusTodo {
		status, note = task.StatusTodo, i18n.F("tasks.reopened", render.Truncate(x.Title, 40))
	}
	return m.write(coord.MTaskStatus, task.TaskStatus{ID: x.ID, Status: status}, note, nil)
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
	if waiting {
		bs = append(bs, btn{keyed(enterKey, i18n.T("tasks.btn_reply")), true, (*Model).openReply})
	}
	if !open && x.Status == task.StatusTodo {
		bs = append(bs, btn{keyed(enterKey, i18n.T("tasks.btn_run")), !waiting, (*Model).openRunDialog})
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
	if r != nil && r.Session != "" {
		bs = append(bs, btn{i18n.T("tasks.btn_take_over"), !open && x.Status != task.StatusTodo, (*Model).takeOver})
		bs = append(bs, btn{keyed(keyName("space"), i18n.T("tasks.btn_view_session")), false, (*Model).viewRunSession})
	}
	bs = append(bs, btn{keyed(keyOf(inList, actEdit), i18n.T("key.edit")), false, func(mm *Model) {
		mm.closeOverlay()
		mm.pending = mm.editTask()
	}})
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
	body := []string{boldSty.Foreground(cText).Render(render.Truncate(x.Title, inner))}
	body = append(body, dimmed.Render(render.Truncate(m.taskWhere(x), inner)))
	body = append(body, frame.Render(strings.Repeat(hRule, inner)))
	if r := m.selectedRun(); r != nil {
		body = append(body, m.runLine(r, inner, false))
		body = append(body, runFacts(r, inner, 4)...)
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

// runFacts: why r ended, what it asked or last noted, and what to do next, at most room lines of question.
func runFacts(r *task.Run, inner, room int) []string {
	var out []string
	if r.Reason != "" && !task.Open(r.State) {
		why := render.RunReason(r.Reason)
		if r.Detail != "" && r.Detail != r.Reason {
			why += " — " + render.Sanitize(r.Detail)
		}
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.reason", why), inner)))
	}
	if r.Ask != "" {
		lines := render.Wrap(i18n.F("tasks.ask", render.Sanitize(r.Ask)), inner)
		if len(lines) > room {
			lines = append(lines[:room-1], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-room+1)))
		}
		for _, l := range lines {
			out = append(out, accent.Render(l))
		}
	} else if r.Note != "" && task.Open(r.State) {
		out = append(out, dimmed.Render(render.Truncate(i18n.F("tasks.note", render.Sanitize(r.Note)), inner)))
	}
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
	return strings.Join(parts, " · ")
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
		took = render.ShortDur(end.Sub(*r.StartedAt))
		if end.Sub(*r.StartedAt) < time.Minute {
			took = end.Sub(*r.StartedAt).Round(time.Second).String()
		}
	}
	text := i18n.F("tasks.run_line", runStateText(r), r.Machine, r.Agent, took)
	if dim {
		sty = dimmed
	}
	return sty.Render(glyph+" ") + render.Truncate(text, max(1, w-3))
}

func runGlyph(r *task.Run) (string, lipgloss.Style) {
	switch r.State {
	case task.Running, task.Starting:
		if r.Attention != "" {
			return render.GlyphWarn, accent
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
	if m.w < compactCols || !m.twoColumn() {
		return m.taskList(y0, 0, m.w, h)
	}
	listW := m.listWidth()
	prevW := m.w - listW - 1
	left := m.taskList(y0, 0, listW, h)
	right := m.taskDetail(prevW, h)
	out := make([]string, h)
	for i := range out {
		out[i] = fit(at(left, i), listW) + " " + fit(at(right, i), prevW)
	}
	return out
}

func (m *Model) taskList(y0, x0, w, h int) []string {
	t := &m.tasks
	title := i18n.F("tasks.title", m.openTaskCount(), len(t.list))
	if t.st != nil {
		if n := len(t.st.NeedsYou()); n > 0 {
			title = i18n.F("tasks.title_needs_you", m.openTaskCount(), len(t.list), n)
		}
	}
	out := []string{fit(dimmed.Render(title), w)}
	room := h - 1
	switch {
	case t.connect == nil:
		return append(out, dimmed.Render(i18n.T("tasks.unavailable")))
	case t.err != nil && !t.loaded:
		out = append(out, errSty.Render(render.Truncate(i18n.F("tasks.no_coordinator", reasonText(t.err)), w)))
		return append(out, dimmed.Render(render.Truncate(i18n.T("tasks.no_coordinator_hint"), w)))
	case !t.loaded:
		return append(out, dimmed.Render(i18n.T("tasks.loading")))
	case len(t.list) == 0:
		return append(out, dimmed.Render(render.Truncate(i18n.F("tasks.empty", keyOf(inList, actNew)), w)))
	}
	if t.cursor < t.scroll {
		t.scroll = t.cursor
	}
	if t.cursor >= t.scroll+room {
		t.scroll = t.cursor - room + 1
	}
	for i := t.scroll; i < len(t.list) && len(out) < h; i++ {
		x, idx := t.list[i], i
		glyph, sty := taskGlyph(m, x)
		cell := ""
		if r := m.lastRun(x.ID); r != nil {
			cell = runStateText(r) + " · " + r.Machine
		}
		titleW := max(4, w-4-render.Width(cell)-2)
		line := " " + sty.Render(glyph) + " " + render.Pad(render.Truncate(x.Title, titleW), titleW) + "  " + dimmed.Render(cell)
		if i == t.cursor {
			line = selTitle.Render(render.Pad(" "+glyph+" "+render.Pad(render.Truncate(x.Title, titleW), titleW)+"  "+cell, w))
		}
		m.mark(y0+len(out), x0, w, func(mm *Model) {
			if mm.tasks.cursor == idx {
				mm.openTask()
			}
			mm.tasks.cursor = idx
		})
		out = append(out, line)
	}
	return out
}

func (m *Model) taskDetail(w, h int) []string {
	x := m.selectedTask()
	if x == nil {
		return nil
	}
	inner := w - 4
	var body []string
	body = append(body, boldSty.Foreground(cText).Render(render.Truncate(x.Title, inner)))
	body = append(body, dimmed.Render(render.Truncate(m.taskWhere(x), inner)))
	if brief := strings.TrimSpace(x.Brief); brief != "" && brief != x.Title {
		body = append(body, "")
		lines := render.Wrap(brief, inner)
		if len(lines) > 6 {
			lines = append(lines[:5], dimmed.Render(i18n.F("tasks.more_lines", len(lines)-5)))
		}
		body = append(body, lines...)
	}
	runs := m.tasks.st.RunsOf(x.ID)
	if len(runs) > 0 {
		body = append(body, "", accent.Render(i18n.F("tasks.runs", len(runs))))
		for i := len(runs) - 1; i >= 0 && i >= len(runs)-4; i-- {
			body = append(body, m.runLine(runs[i], inner, i != len(runs)-1))
			if i == len(runs)-1 {
				body = append(body, runFacts(runs[i], inner, 3)...)
			}
		}
	}
	if r := m.selectedRun(); r != nil {
		room := h - 2 - len(body) - 2
		if room > 2 {
			body = append(body, "", accent.Render(i18n.F("tasks.output", r.ID)))
			o, ok := m.tasks.out[r.ID]
			var lines []string
			for l := range strings.Lines(strings.TrimRight(o.text, "\n")) {
				lines = append(lines, render.Wrap(strings.TrimRight(l, "\r\n"), inner)...)
			}
			switch {
			case !ok:
				lines = []string{dimmed.Render(i18n.T("tasks.loading"))}
			case len(lines) == 0:
				lines = []string{dimmed.Render(i18n.T("tasks.no_output"))}
			}
			if len(lines) > room {
				lines = lines[len(lines)-room:]
			}
			body = append(body, lines...)
		}
	}
	return panel(i18n.T("tasks.detail"), body, w, h)
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

package tui

import (
	"cmp"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
)

// sessionTasks: by agent session id, the task of the newest run that used it; a run still queued to continue a session
// counts for it.
func sessionTasks(st *task.State) map[string]*task.Task {
	newest := map[string]*task.Run{}
	for _, r := range st.Runs {
		s := cmp.Or(r.Session, r.Resume)
		if s == "" || st.Tasks[r.Task] == nil {
			continue
		}
		if o := newest[s]; o == nil || r.Seq > o.Seq || r.Seq == o.Seq && r.QueuedAt.After(o.QueuedAt) {
			newest[s] = r
		}
	}
	out := make(map[string]*task.Task, len(newest))
	for s, r := range newest {
		out[s] = st.Tasks[r.Task]
	}
	return out
}

// taskLabel: id, title and stage on one line; the title comes from agents and trackers.
func taskLabel(x *task.Task) string {
	s := x.ID + " " + strings.Join(strings.Fields(render.Sanitize(x.Title)), " ")
	if x.Stage != "" {
		s += " · " + strings.Join(strings.Fields(render.Sanitize(x.Stage)), " ")
	}
	return s
}

// linkKey is the state the session links were derived from.
type linkKey struct {
	st   *task.State
	seq  int64
	runs int
}

// taskOf: the task r's session worked for, nil while the TUI holds no task state.
func (m *Model) taskOf(r *tend.Rec) *task.Task {
	t := &m.tasks
	if t.st == nil || r == nil || r.SessionID == "" {
		return nil
	}
	if k := (linkKey{t.st, t.st.Seq, len(t.st.Runs)}); t.linkedAt != k {
		t.links, t.linkedAt = sessionTasks(t.st), k
	}
	return t.links[r.SessionID]
}

// taskText: what the sessions search matches of r's task besides r itself.
func (m *Model) taskText(r *tend.Rec) string {
	if x := m.taskOf(r); x != nil {
		return x.ID + " " + x.Title
	}
	return ""
}

// taskGroup: the resume dialog's way to the task r's session worked for.
func (m *Model) taskGroup(r *tend.Rec) []btnGroup {
	x := m.taskOf(r)
	if x == nil {
		return nil
	}
	label := keyed(keyOf(inResume, actTask), i18n.F("resume.btn_task", x.ID))
	return []btnGroup{{label: i18n.T("resume.group.task"), bs: []btn{{label, false, (*Model).openLinkedTask}}}}
}

// openLinkedTask: the Tasks view on the dialog's session's task; a search hiding it there is cleared.
func (m *Model) openLinkedTask() {
	x := m.taskOf(m.ov.rec)
	if x == nil {
		return
	}
	m.closeOverlay()
	m.setView(viewTasks)
	if !m.selectTaskID(x.ID) {
		m.search.SetValue("")
		m.filterTasks()
		m.selectTaskID(x.ID)
	}
}

func (m *Model) selectTaskID(id string) bool {
	for i, y := range m.tasks.list {
		if y.ID == id {
			m.tasks.cursor = i
			return true
		}
	}
	return false
}

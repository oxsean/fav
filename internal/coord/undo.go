package coord

import (
	"encoding/json"
	"slices"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// TaskUndo takes back Command, the caller's own task.set_status or task.move of task ID.
type TaskUndo struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// undoable is what taking back a command needs: the task as it stood before it, the rev it left the task at, and the
// merge run a done queued instead of finishing the task.
type undoable struct {
	was task.TaskRestore
	rev int
	run string
}

// undoOf is how env's command, if one an undo takes back, would be undone; read before env applies. The caller holds
// mu.
func (c *Coord) undoOf(env journal.Envelope) *undoable {
	if env.Command == nil || len(env.Events) == 0 || (env.Command.Method != MTaskStatus && env.Command.Method != MTaskMove) {
		return nil
	}
	e := env.Events[0]
	var id, run string
	switch e.Type {
	case task.ETaskStatus:
		var d task.TaskStatus
		if json.Unmarshal(e.Data, &d) != nil || d.Status == task.StatusCanceled {
			return nil
		}
		id = d.ID
	case task.ETaskMoved:
		var d task.TaskMove
		if json.Unmarshal(e.Data, &d) != nil {
			return nil
		}
		id = d.ID
	case task.ERunQueued:
		var d task.Run
		if json.Unmarshal(e.Data, &d) != nil || d.Stage != task.StageMerge {
			return nil
		}
		id, run = d.Task, d.ID
	default:
		return nil
	}
	t := c.st.Tasks[id]
	if t == nil {
		return nil
	}
	return &undoable{was: task.RestoreOf(t), run: run}
}

// keepUndo records u, the undo of env's command, once env applied. The caller holds mu.
func (c *Coord) keepUndo(env journal.Envelope, u *undoable) {
	if u == nil {
		return
	}
	if t := c.st.Tasks[u.was.ID]; t != nil {
		u.rev = t.Rev
		c.undos[receiptKey(env.Who().ID, env.Command.ID)] = u
	}
}

// taskUndo is task.undo: the task goes back to how it stood before the command, field for field, when nothing changed
// it since and its old place still takes it; what the coordinator did because of the command stays done. A done that queued a merge is taken back by
// canceling the merge while it waits.
func (c *Coord) taskUndo(who Principal, r *wire.Request) (string, []journal.Event, error) {
	var p TaskUndo
	if err := r.Decode(&p); err != nil {
		return "", nil, err
	}
	t, err := c.writableTask(who, p.ID)
	if err != nil {
		return "", nil, err
	}
	u := c.undos[receiptKey(who.User, p.Command)]
	if u == nil || u.was.ID != t.ID {
		return "", nil, notFound("command " + p.Command)
	}
	if t.Rev != u.rev {
		return "", nil, conflict("changed")
	}
	if u.run != "" {
		if run := c.st.Runs[u.run]; run == nil || run.State != task.Queued {
			return "", nil, conflict("merge started")
		}
		return t.ID, []journal.Event{journal.NewEvent(task.ERunCanceled, task.RunRef{ID: u.run, Reason: task.WhyUndone})}, nil
	}
	if u.was.Parent != t.Parent || !slices.Equal(u.was.After, t.After) {
		if err := c.checkPlace(who, t, u.was.Parent, u.was.After); err != nil {
			return "", nil, err
		}
	}
	return t.ID, []journal.Event{journal.NewEvent(task.ETaskRestored, u.was)}, nil
}

package tui

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/wire"
)

// outFeed is a run's output as run.output.watch brings it while the Tasks view shows the run.
type outFeed struct {
	w       *wire.Watch
	opening bool
	events  []output.Event
	cursor  *node.Cursor
	loaded  bool      // the stream opened
	end     bool      // it finished: the run ended and all of its output came
	failed  time.Time // it broke then: it opens again from cursor once outRetry passed
}

const (
	outKeep  = 1000 // events a feed keeps
	outRetry = 5 * time.Second
)

// syncOutputs watches the output of the runs the view shows, the selected task's last run and the one watched, and
// lets go of the others.
func (m *Model) syncOutputs() tea.Cmd {
	t := &m.tasks
	want := map[string]bool{}
	if m.view == viewTasks && t.cl != nil {
		for _, r := range []*task.Run{m.selectedRun(), m.watchedRun()} {
			if r != nil {
				want[r.ID] = true
			}
		}
	}
	for id, f := range t.out {
		if !want[id] {
			if f.w != nil {
				f.w.Cancel()
			}
			delete(t.out, id)
		}
	}
	var cmds []tea.Cmd
	for id := range want {
		if t.out == nil {
			t.out = map[string]*outFeed{}
		}
		f := t.out[id]
		if f == nil {
			f = &outFeed{}
			t.out[id] = f
		}
		if f.w != nil || f.opening || f.end || !f.failed.IsZero() && time.Since(f.failed) < outRetry {
			continue
		}
		f.opening, f.failed = true, time.Time{}
		cl, p := t.cl, coord.OutputWatchParams{Run: id, From: f.cursor}
		cmds = append(cmds, func() tea.Msg {
			return nextOutput(cl, id, cl.Watch(context.Background(), coord.MRunOutputWatch, p))
		})
	}
	return tea.Batch(cmds...)
}

type outPushMsg struct {
	cl     *coord.Client
	run    string
	w      *wire.Watch
	pushes []wire.Push
	err    error // the stream ended
}

// nextOutput waits for the next pushes of w, all that are there, or its end.
func nextOutput(cl *coord.Client, run string, w *wire.Watch) outPushMsg {
	s := nextState(cl, w, 0)
	return outPushMsg{cl: cl, run: run, w: w, pushes: s.pushes, err: s.err}
}

func (msg outPushMsg) apply(m *Model) tea.Cmd {
	t := &m.tasks
	f := t.out[msg.run]
	if f == nil || t.cl != msg.cl || f.w != nil && f.w != msg.w {
		msg.w.Cancel()
		return nil
	}
	f.w, f.opening = msg.w, false
	for _, p := range msg.pushes {
		f.take(p)
	}
	switch {
	case msg.err == nil:
		return func() tea.Msg { return nextOutput(msg.cl, msg.run, msg.w) }
	case errors.Is(msg.err, io.EOF):
		f.end = true
	case wire.Code(msg.err) == wire.CodeUnauthorized:
		delete(t.out, msg.run)
		return nil
	default:
		tracef("tasks: output of %s ended: %v", msg.run, msg.err)
		f.failed = time.Now()
	}
	f.w = nil
	return nil
}

// take applies one push: an event with a key replaces the one before it with that key; the cursor moves as the push
// says.
func (f *outFeed) take(p wire.Push) {
	switch p.Method {
	case wire.PushOpen:
		var o struct {
			Mode string       `json:"mode"`
			From *output.Pos  `json:"from"`
			To   *output.Pos  `json:"to"`
			At   *node.Cursor `json:"cursor"`
		}
		if p.Decode(&o) != nil {
			return
		}
		f.loaded = true
		if o.Mode == wire.ModeGap {
			f.events = append(f.events, output.Event{Kind: output.KindGap, From: o.From, To: o.To})
		}
		if o.At != nil {
			f.cursor = o.At
		}
	case coord.PushOutput:
		var op coord.OutputPush
		if p.Decode(&op) != nil {
			return
		}
		for _, e := range op.Events {
			i := keyedAt(f.events, e.Key)
			switch {
			case e.Temp && e.Text == "" && e.Title == "": // a message or command that ended without its final event
				if i >= 0 {
					f.events = slices.Delete(f.events, i, i+1)
				}
			case i >= 0:
				f.events[i] = e
			default:
				f.events = append(f.events, e)
			}
		}
		if len(f.events) > outKeep {
			f.events = f.events[len(f.events)-outKeep:]
		}
		if op.Cursor != nil {
			f.cursor = op.Cursor
		}
	}
}

// keyedAt is where the event with key is, from the end; -1 when none is (or key is "").
func keyedAt(evs []output.Event, key string) int {
	if key == "" {
		return -1
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Key == key {
			return i
		}
	}
	return -1
}

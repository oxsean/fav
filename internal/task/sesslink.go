package task

import "cmp"

// RunSession is the agent session a run used, or is queued to continue.
func RunSession(r *Run) string { return cmp.Or(r.Session, r.Resume) }

// Newest keeps, per key, the newest run added: the higher Seq, then the later QueuedAt.
type Newest[K comparable] map[K]*Run

func (n Newest[K]) Add(k K, r *Run) {
	if o := n[k]; o == nil || r.Seq > o.Seq || r.Seq == o.Seq && r.QueuedAt.After(o.QueuedAt) {
		n[k] = r
	}
}

// SessionTasks: by agent session id, the task of the newest run that used it; runs of a missing task are skipped.
func SessionTasks(st *State) map[string]*Task {
	newest := Newest[string]{}
	for _, r := range st.Runs {
		if s := RunSession(r); s != "" && st.Tasks[r.Task] != nil {
			newest.Add(s, r)
		}
	}
	out := make(map[string]*Task, len(newest))
	for s, r := range newest {
		out[s] = st.Tasks[r.Task]
	}
	return out
}

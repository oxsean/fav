package task

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
)

// world is a state and a clock, with the coordinator's part played by settle.
type world struct {
	t   *testing.T
	s   *State
	at  time.Time
	ids int
}

func newWorld(t *testing.T) *world {
	return &world{t: t, s: New(), at: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)}
}

func (w *world) do(events ...journal.Event) {
	w.t.Helper()
	w.at = w.at.Add(time.Second)
	if err := w.s.Apply(journal.Envelope{Seq: w.s.Seq + 1, At: w.at, Events: events}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) task(parent string, after []string, status string) string {
	w.ids++
	id := fmt.Sprintf("t_%02d", w.ids)
	w.do(journal.NewEvent(ETaskCreated, Task{ID: id, Title: id, Parent: parent, After: after, Status: status}))
	return id
}

// settle plays the coordinator until nothing is left for it: it dispatches what is ready (or holds it when hold says
// so) and marks done what succeeded. It returns the runs it queued.
func (w *world) settle(hold func(*Task) bool) []string {
	var queued []string
	for range 100 {
		var events []journal.Event
		for _, t := range w.s.Ready() {
			if hold != nil && hold(t) {
				events = append(events, journal.NewEvent(ETaskHeld, TaskHold{ID: t.ID, Reason: "no_agent"}))
				continue
			}
			w.ids++
			id := fmt.Sprintf("r_%02d", w.ids)
			queued = append(queued, id)
			events = append(events, journal.NewEvent(ERunQueued, Run{ID: id, Task: t.ID, Machine: "m"}))
		}
		for _, t := range w.s.Completing() {
			events = append(events, journal.NewEvent(ETaskStatus, TaskStatus{ID: t.ID, Status: StatusDone}))
		}
		for _, t := range w.s.Advancing() {
			if next := t.Flow.Next(t.Stage); next != "" {
				events = append(events, journal.NewEvent(ETaskStaged, TaskStage{ID: t.ID, Stage: next, Loops: t.Loops}))
			} else {
				events = append(events, journal.NewEvent(ETaskStatus, TaskStatus{ID: t.ID, Status: StatusDone}))
			}
		}
		for _, t := range w.s.Reworking() {
			events = append(events, journal.NewEvent(ETaskStaged, TaskStage{ID: t.ID, Stage: t.Flow.Back(t.Stage), Loops: t.Loops + 1}))
		}
		if len(events) == 0 {
			return queued
		}
		w.do(events...)
	}
	w.t.Fatal("the coordinator never settles")
	return nil
}

func (w *world) end(run string, state string, code int, attention string) {
	w.endWith(run, Observation{ID: run, State: state, NodeRev: 9, Attention: attention}, code)
}

func (w *world) endWith(run string, o Observation, code int) {
	state := o.State
	if state == Exited {
		o.ExitCode = &code
	}
	w.do(journal.NewEvent(ERunStarting, RunStarting{ID: run}), journal.NewEvent(ERunObserved, o))
}

func TestAThreeLevelTreeRunsByItsDependencies(t *testing.T) {
	w := newWorld(t)
	root := w.task("", nil, StatusBacklog)
	api := w.task(root, nil, StatusBacklog)
	model := w.task(api, nil, StatusBacklog)
	handler := w.task(api, []string{model}, StatusBacklog)
	ui := w.task(root, []string{api}, StatusBacklog)
	if q := w.settle(nil); len(q) != 0 {
		t.Fatalf("nothing in the backlog runs: %v", q)
	}
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{root, api, model, handler, ui}}))
	order := []string{}
	for range 5 {
		if len(order) == 2 { // api's subtasks are done: it waits to be accepted, and ui waits for it
			w.settle(nil)
			if sit := w.s.Situation(w.s.Tasks[api]); sit.Reason != WhyAccept {
				t.Fatal(sit)
			}
			w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: api, Status: StatusDone}))
		}
		q := w.settle(nil)
		if len(q) != 1 {
			sits := map[string]Situation{}
			for id, x := range w.s.Tasks {
				sits[id] = w.s.Situation(x)
			}
			t.Fatalf("one leaf at a time here, got %v: %v", q, sits)
		}
		order = append(order, w.s.Runs[q[0]].Task)
		w.end(q[0], Exited, 0, "")
		if len(order) == 3 {
			break
		}
	}
	w.settle(nil)
	if want := []string{model, handler, ui}; !slices.Equal(order, want) {
		t.Fatalf("dispatch order %v, want %v", order, want)
	}
	for _, id := range []string{model, handler, ui} {
		if w.s.Tasks[id].Status != StatusDone {
			t.Fatalf("%s: %s", id, w.s.Tasks[id].Status)
		}
	}
	if sit := w.s.Situation(w.s.Tasks[root]); sit != (Situation{Kind: SitWaiting, Reason: WhyAccept}) || w.s.Latest(root) != nil || w.s.Latest(api) != nil {
		t.Fatalf("a parent never runs and waits to be accepted: %+v", sit)
	}
}

func TestAFailedTryWaitsAndAStartTriesAgain(t *testing.T) {
	w := newWorld(t)
	a := w.task("", nil, StatusTodo)
	b := w.task("", []string{a}, StatusTodo)
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{a, b}}))
	q := w.settle(nil)
	w.end(q[0], Exited, 1, "")
	if q := w.settle(nil); len(q) != 0 {
		t.Fatalf("a failure is not retried on its own: %v", q)
	}
	if sit := w.s.Situation(w.s.Tasks[a]); sit.Kind != SitWaiting || sit.Reason != Exited {
		t.Fatal(sit)
	}
	if sit := w.s.Situation(w.s.Tasks[b]); sit != (Situation{Kind: SitQueued, Reason: WhyAfter}) {
		t.Fatal(sit)
	}
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{a}}))
	if q := w.settle(nil); len(q) != 1 || w.s.Runs[q[0]].Task != a {
		t.Fatalf("starting it again tries again: %v", q)
	}
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: a, Status: StatusCanceled}))
	if sit := w.s.Situation(w.s.Tasks[b]); sit.Reason != WhyAfterCanceled {
		t.Fatal(sit)
	}
}

func TestAHeldTaskWaitsUntilItIsEditedOrStarted(t *testing.T) {
	w := newWorld(t)
	a := w.task("", nil, StatusTodo)
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{a}}))
	w.settle(func(*Task) bool { return true })
	if sit := w.s.Situation(w.s.Tasks[a]); sit.Reason != WhyHeld || w.s.Tasks[a].Held != "no_agent" {
		t.Fatal(sit, w.s.Tasks[a].Held)
	}
	title := "y"
	w.do(journal.NewEvent(ETaskEdited, TaskEdit{ID: a, Title: &title}))
	if q := w.settle(nil); len(q) != 1 {
		t.Fatal("an edit lets it go again")
	}
}

// TestEveryUnfinishedTaskStandsOneWayWithAReason drives random histories: tasks made, moved, started, their runs
// ending every way, statuses set by hand; after the coordinator settles, each started task that is not finished is
// running, queued or waiting, with a reason, and nothing is left for the coordinator to do.
func TestEveryUnfinishedTaskStandsOneWayWithAReason(t *testing.T) {
	seen := map[string]int{}
	defer func() {
		for _, k := range []string{SitRunning + "/running", SitQueued + "/" + WhyAfter, SitQueued + "/" + WhyChildren, SitQueued + "/" + WhySlot,
			SitWaiting + "/" + WhyAccept, SitWaiting + "/" + WhyHeld, SitWaiting + "/" + WhyAfterCanceled, SitWaiting + "/" + WhyDispatch,
			SitWaiting + "/" + AttentionAsked, SitWaiting + "/" + AttentionPermission, SitWaiting + "/access_revoked", SitDone + "/",
			SitWaiting + "/" + WhySourceChanged, SitWaiting + "/" + WhySourceClosed,
			SitWaiting + "/" + WhyMaxLoops, SitWaiting + "/" + WhyBlocked} {
			if seen[k] == 0 {
				t.Errorf("the histories never reach %s: %v", k, seen)
			}
		}
	}()
	for seed := range uint64(300) {
		rng := rand.New(rand.NewPCG(seed, 7))
		w := newWorld(t)
		pick := func(ok func(*Task) bool) *Task {
			var c []*Task
			for _, id := range sortedIDs(w.s) {
				if x := w.s.Tasks[id]; ok(x) {
					c = append(c, x)
				}
			}
			if len(c) == 0 {
				return nil
			}
			return c[rng.IntN(len(c))]
		}
		openRuns := func() []*Run {
			var out []*Run
			for _, r := range w.s.Runs {
				if Open(r.State) {
					out = append(out, r)
				}
			}
			slices.SortFunc(out, func(a, b *Run) int { return int(a.Seq - b.Seq) })
			return out
		}
		for step := range 60 {
			switch rng.IntN(10) {
			case 0, 1: // a task, maybe under another, maybe after others
				parent := ""
				if p := pick(func(x *Task) bool {
					return rng.IntN(2) == 0 && w.s.Depth(x.ID) < MaxDepth && w.s.OpenRun(x.ID) == nil && !Finished(x.Status)
				}); p != nil {
					parent = p.ID
				}
				var after []string
				if a := pick(func(*Task) bool { return rng.IntN(3) == 0 }); a != nil {
					after = []string{a.ID}
				}
				id := w.task(parent, after, []string{StatusBacklog, StatusTodo}[rng.IntN(2)])
				switch rng.IntN(4) {
				case 0:
					if parent == "" { // a requirement from an issue
						w.s.Tasks[id].Source = &Source{Kind: "gitea", Number: int64(w.ids), Rev: 1, Digest: "d0", Seen: "d0", SeenRev: 1}
					}
				case 1, 2: // in a workflow
					x := w.s.Tasks[id]
					x.Flow, x.Workflow, x.Stage, x.StageSeq = testFlow(), "feature", "implement", w.s.Seq
					x.Flow.MaxLoops = []int{0, 0, 1}[rng.IntN(3)]
				}
			case 2: // started, with its subtree
				if x := pick(func(x *Task) bool { return !Finished(x.Status) }); x != nil {
					var ids []string
					for _, y := range w.s.Subtree(x.ID) {
						if !Finished(y.Status) {
							ids = append(ids, y.ID)
						}
					}
					w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: ids}))
				}
			case 3, 4, 5: // a run ends, one way or another
				if rs := openRuns(); len(rs) > 0 {
					r := rs[rng.IntN(len(rs))]
					switch rng.IntN(8) {
					case 7:
						w.do(journal.NewEvent(ERunStarting, RunStarting{ID: r.ID}), journal.NewEvent(ERunObserved,
							Observation{ID: r.ID, State: Running, NodeRev: 3}))
					case 0:
						w.end(r.ID, Exited, 0, "")
					case 1:
						w.endWith(r.ID, Observation{ID: r.ID, State: Exited, NodeRev: 9,
							Verdict: &agent.Verdict{Verdict: []string{"pass", "rework", "rework", "blocked"}[rng.IntN(4)]}}, 0)
					case 2:
						w.end(r.ID, Exited, 1, "")
					case 3:
						w.end(r.ID, Exited, 0, AttentionAsked)
					case 4:
						w.end(r.ID, Failed, 0, "")
					case 5:
						w.do(journal.NewEvent(ERunCanceled, RunRef{ID: r.ID, Reason: "access_revoked"}))
					default:
						w.do(journal.NewEvent(ERunStarting, RunStarting{ID: r.ID}), journal.NewEvent(ERunObserved,
							Observation{ID: r.ID, State: Running, NodeRev: 5, Attention: AttentionPermission}))
					}
				}
			case 6: // someone sets a status by hand
				if x := pick(func(x *Task) bool { return !Finished(x.Status) && w.s.OpenRun(x.ID) == nil }); x != nil {
					w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: x.ID, Status: []string{StatusDone, StatusCanceled, StatusTodo}[rng.IntN(3)]}))
				}
			case 8: // the sync worker reads an issue: changed, closed or reopened
				if x := pick(func(x *Task) bool { return x.Source != nil }); x != nil {
					w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: x.ID, Digest: fmt.Sprintf("d%d", rng.IntN(3)), Title: "t",
						Closed: rng.IntN(4) == 0}))
				}
			case 9: // someone takes or declines what changed
				if x := pick(func(x *Task) bool { return SourceWaits(x) != "" }); x != nil {
					w.do(journal.NewEvent(ETaskSourceAcked, SourceAck{ID: x.ID, Accept: rng.IntN(2) == 0}))
				}
			case 7: // moved after another task, without a cycle
				x, y := pick(func(*Task) bool { return true }), pick(func(*Task) bool { return true })
				if x != nil && y != nil && x != y && !w.s.Reaches(y.ID, x.ID) {
					after := []string{y.ID}
					w.do(journal.NewEvent(ETaskMoved, TaskMove{ID: x.ID, After: &after}))
				}
			}
			w.settle(func(x *Task) bool { return rng.IntN(10) == 0 })
			for _, id := range sortedIDs(w.s) {
				x := w.s.Tasks[id]
				sit := w.s.Situation(x)
				where := fmt.Sprintf("seed %d step %d %s", seed, step, id)
				seen[sit.Kind+"/"+sit.Reason]++
				switch {
				case x.Status == StatusBacklog || Finished(x.Status):
					if sit.Kind != x.Status {
						t.Fatalf("%s: %s stands %+v", where, x.Status, sit)
					}
				case !slices.Contains([]string{SitRunning, SitQueued, SitWaiting}, sit.Kind) || sit.Reason == "":
					t.Fatalf("%s: %+v", where, sit)
				case sit.Reason == WhyReady || sit.Reason == WhyCompleting || sit.Reason == WhyAdvance || sit.Reason == WhyRework:
					t.Fatalf("%s: left for the coordinator: %+v", where, sit)
				}
				n := 0
				for _, r := range w.s.Runs {
					if r.Task == id && Open(r.State) {
						n++
					}
				}
				if n > 1 {
					t.Fatalf("%s: %d open runs", where, n)
				}
				if w.s.Depth(id) > MaxDepth {
					t.Fatalf("%s: depth %d", where, w.s.Depth(id))
				}
			}
		}
	}
}

func sortedIDs(s *State) []string {
	var ids []string
	for id := range s.Tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func TestAnIssueThatChangesOrClosesWaitsForSomeone(t *testing.T) {
	w := newWorld(t)
	w.do(journal.NewEvent(ETaskCreated, Task{ID: "t_r", Title: "v1", Brief: "one", Kind: KindRequirement, Status: StatusTodo,
		Source: &Source{Kind: "gitea", Number: 7, Rev: 1, Digest: "a", Seen: "a", SeenRev: 1}}))
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{"t_r"}}))
	if sit := w.s.Situation(w.s.Tasks["t_r"]); sit.Reason != WhyReady {
		t.Fatalf("an issue read again unchanged changes nothing: %+v", sit)
	}
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "b", Title: "v2", Text: "two"}))
	x := w.s.Tasks["t_r"]
	if sit := w.s.Situation(x); sit.Reason != WhySourceChanged || x.Title != "v1" || x.Source.Pending.Rev != 2 {
		t.Fatalf("a change waits, the scope stays: %+v %+v", sit, x.Source)
	}
	w.do(journal.NewEvent(ETaskSourceAcked, SourceAck{ID: "t_r"}))
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "b", Title: "v2", Text: "two"}))
	if sit := w.s.Situation(x); sit.Reason != WhyReady || x.Title != "v1" || x.Source.Rev != 1 {
		t.Fatalf("a declined revision is not raised again: %+v %+v", sit, x.Source)
	}
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "c", Title: "v3", Text: "three"}))
	w.do(journal.NewEvent(ETaskSourceAcked, SourceAck{ID: "t_r", Accept: true}))
	if x.Title != "v3" || x.Brief != "three" || x.Source.Rev != 3 || x.Source.Digest != "c" {
		t.Fatalf("a taken revision becomes the scope: %+v %+v", x, x.Source)
	}
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "c", Closed: true}))
	if sit := w.s.Situation(x); sit.Reason != WhySourceClosed {
		t.Fatalf("closed outside tend: %+v", sit)
	}
	w.do(journal.NewEvent(ETaskSourceAcked, SourceAck{ID: "t_r"}))
	if sit := w.s.Situation(x); sit.Reason != WhyReady {
		t.Fatalf("kept going: %+v", sit)
	}
	w.do(journal.NewEvent(ETaskStatus, TaskStatus{ID: "t_r", Status: StatusDone}))
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "c", Closed: false}))
	w.do(journal.NewEvent(ETaskSourced, SourceUpdate{ID: "t_r", Digest: "c", Closed: true}))
	if sit := w.s.Situation(x); sit.Kind != SitDone {
		t.Fatalf("a done task stays done: %+v", sit)
	}
}

func testFlow() *Flow {
	return &Flow{Name: "feature", MaxLoops: 1, Stages: []Stage{{Name: "implement", Role: "implement"},
		{Name: "review", Role: "review", Output: OutputVerdict}, {Name: "accept", Gate: GateHuman}}}
}

func TestAWorkflowGoesStageByStageAndBackOnRework(t *testing.T) {
	w := newWorld(t)
	w.do(journal.NewEvent(ETaskCreated, Task{ID: "t_f", Title: "f", Status: StatusTodo, Workflow: "feature", Flow: testFlow(), Stage: "implement"}))
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{"t_f"}}))
	x := w.s.Tasks["t_f"]
	stage := func(want, reason string) {
		t.Helper()
		if sit := w.s.Situation(x); x.Stage != want || sit.Reason != reason {
			t.Fatalf("stage %s %+v, want %s %s", x.Stage, sit, want, reason)
		}
	}
	verdict := func(v string) *agent.Verdict { return &agent.Verdict{Verdict: v} }
	runs := w.settle(nil)
	w.end(runs[0], Exited, 0, "")
	runs = w.settle(nil)
	stage("review", WhySlot)
	w.endWith(runs[0], Observation{ID: runs[0], State: Exited, NodeRev: 9}, 0)
	stage("review", WhyBlocked)
	w.do(journal.NewEvent(ETaskStarted, TaskStart{IDs: []string{"t_f"}}))
	runs = w.settle(nil)
	w.endWith(runs[0], Observation{ID: runs[0], State: Exited, NodeRev: 9, Verdict: verdict("rework")}, 0)
	runs = w.settle(nil)
	if x.Loops != 1 {
		t.Fatalf("a rework counts: %+v", x)
	}
	stage("implement", WhySlot)
	w.endWith(runs[0], Observation{ID: runs[0], State: Exited, NodeRev: 9, Check: &agent.CheckResult{Exit: 1}}, 0)
	stage("implement", WhyMaxLoops)
	w.do(journal.NewEvent(ETaskStaged, TaskStage{ID: "t_f", Stage: "review", Loops: 1}))
	runs = w.settle(nil)
	w.endWith(runs[0], Observation{ID: runs[0], State: Exited, NodeRev: 9, Verdict: verdict("pass")}, 0)
	w.settle(nil)
	stage("accept", WhyAccept)
}

func TestAQueuedRunSaysWhenAnotherRunHoldsItsDirectory(t *testing.T) {
	w := newWorld(t)
	a, b, c := w.task("", nil, StatusTodo), w.task("", nil, StatusTodo), w.task("", nil, StatusTodo)
	w.do(journal.NewEvent(ERunQueued, Run{ID: "r_a", Task: a, Machine: "m", Dir: "/w/app"}),
		journal.NewEvent(ERunQueued, Run{ID: "r_b", Task: b, Machine: "m", Dir: "/w/app"}),
		journal.NewEvent(ERunQueued, Run{ID: "r_c", Task: c, Machine: "m", Dir: "/w/other"}))
	w.do(journal.NewEvent(ERunObserved, Observation{ID: "r_a", State: Running}))
	if sit := w.s.Situation(w.s.Tasks[b]); sit != (Situation{Kind: SitQueued, Reason: WhyDir, Run: "r_b"}) {
		t.Fatal("same directory:", sit)
	}
	if sit := w.s.Situation(w.s.Tasks[c]); sit != (Situation{Kind: SitQueued, Reason: WhySlot, Run: "r_c"}) {
		t.Fatal("another directory:", sit)
	}
	w.do(journal.NewEvent(ERunObserved, Observation{ID: "r_a", State: Exited}))
	if sit := w.s.Situation(w.s.Tasks[b]); sit.Reason != WhySlot {
		t.Fatal("the directory is free:", sit)
	}
}

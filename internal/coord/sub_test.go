package coord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// watched is a state.watch its test reads push by push.
type watched struct {
	t      *testing.T
	w      *wire.Watch
	pushes chan wire.Push
	head   *wire.Push // the open push, read before watchState returned
}

func watchState(t *testing.T, cli *wire.Conn, wp WatchParams) *watched {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &watched{t: t, w: cli.Watch(ctx, MStateWatch, wp), pushes: make(chan wire.Push, 4096)}
	go func() {
		defer close(w.pushes)
		for {
			p, err := w.w.Next(context.Background())
			if err != nil {
				return
			}
			w.pushes <- p
		}
	}()
	open := w.next() // the coordinator has the watch: what the test does next is in its snapshot or pushed after it
	w.head = &open
	return w
}

func (w *watched) next() wire.Push {
	w.t.Helper()
	if p := w.head; p != nil {
		w.head = nil
		return *p
	}
	select {
	case p, ok := <-w.pushes:
		if !ok {
			w.t.Fatalf("the stream ended: %v", w.w.Err())
		}
		return p
	case <-time.After(10 * time.Second):
		w.t.Fatal("no push")
	}
	return wire.Push{}
}

// journal is the next journal push, skipping the rest.
func (w *watched) journal() journal.Envelope {
	w.t.Helper()
	for {
		if p := w.next(); p.Method == PushJournal {
			var env journal.Envelope
			if err := p.Decode(&env); err != nil {
				w.t.Fatal(err)
			}
			return env
		}
	}
}

// fold folds pushes until the copy reaches seq.
func (w *watched) fold(f *StateFold, seq int64) {
	w.t.Helper()
	for f.St == nil || f.St.Seq < seq {
		if _, err := f.Apply(w.next()); err != nil {
			w.t.Fatal(err)
		}
	}
}

func methods(ps []wire.Push) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Method)
	}
	return strings.Join(out, " ")
}

// shape is a stream's pushes as a sequence of methods with repeats collapsed: open snapshot+ live journal*.
func shape(ms []string) string {
	var out []string
	for _, m := range ms {
		if len(out) == 0 || out[len(out)-1] != m {
			out = append(out, m)
		}
	}
	return strings.Join(out, " ")
}

type frameLine struct {
	C *wire.Frame `json:"c"`
	S *wire.Frame `json:"s"`
}

func frames(t *testing.T, name string) []frameLine {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "server", "webtest", "frames", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []frameLine
	for _, line := range bytes.Split(b, []byte("\n")) {
		var l frameLine
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &l) != nil || l.C == nil && l.S == nil {
			continue
		}
		out = append(out, l)
	}
	return out
}

func strict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// TestTheWebClientsStateFramesAreTheCoordinators: every state.watch push in the web client's frame files decodes into
// the Go types with nothing left over, parts included.
func TestTheWebClientsStateFramesAreTheCoordinators(t *testing.T) {
	for _, name := range []string{"state-snapshot", "state-resume", "state-restart"} {
		watches := map[int64]bool{}
		for _, l := range frames(t, name) {
			if l.C != nil {
				if l.C.Method == MStateWatch {
					watches[l.C.ID] = true
					var wp WatchParams
					if err := strict(l.C.Params, &wp); err != nil {
						t.Fatalf("%s: %s: %v", name, l.C.Params, err)
					}
				}
				continue
			}
			f := l.S
			if f.Type != wire.TypePush || !watches[f.ID] {
				continue
			}
			var err error
			switch f.Method {
			case wire.PushOpen:
				err = strict(f.Params, &wire.Open{})
			case PushReset:
				err = strict(f.Params, &struct{}{})
			case PushLive:
				err = strict(f.Params, &Live{})
			case PushJournal:
				err = strict(f.Params, &journal.Envelope{})
			case PushSnapshot:
				var sn Snapshot
				if err = strict(f.Params, &sn); err == nil && sn.Part != "nope" { // state-restart: a part no one knows
					err = strictTake(sn)
				}
			default:
				t.Fatalf("%s: a push the coordinator does not send: %s", name, f.Method)
			}
			if err != nil {
				t.Fatalf("%s: %s %s: %v", name, f.Method, f.Params, err)
			}
		}
	}
}

func strictTake(sn Snapshot) error {
	for _, b := range sn.Items {
		var v any
		switch sn.Part {
		case task.PartProjects:
			v = &task.Project{}
		case task.PartTasks:
			v = &task.Task{}
		case task.PartRuns:
			v = &task.Run{}
		case task.PartShares:
			v = &task.Share{}
		case task.PartAgentDefs:
			v = &task.AgentDef{}
		case task.PartAffordances:
			v = &[]string{}
		default:
			return errors.New("part " + sn.Part)
		}
		if err := strict(b, v); err != nil {
			return err
		}
	}
	return task.New().Take(sn.Part, sn.Items)
}

// TestStateWatchSpeaksAsTheFrameFilesShow: opened with the files' params, the coordinator's pushes take the shape the
// web client expects.
func TestStateWatchSpeaksAsTheFrameFilesShow(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	for range 6 {
		e.task("t", "quick")
	}
	want := func(name string, id int64) string {
		var ms []string
		for _, l := range frames(t, name) {
			if l.S != nil && l.S.ID == id && l.S.Type == wire.TypePush {
				ms = append(ms, l.S.Method)
				if l.S.Method == PushJournal { // what follows depends on the journal
					break
				}
			}
		}
		return shape(ms)
	}
	read := func(w *watched, live func()) string {
		var ms []string
		for {
			p := w.next()
			ms = append(ms, p.Method)
			if p.Method == PushLive && live != nil {
				live() // before live it could land in the snapshot and push no journal
			}
			if p.Method == PushJournal {
				return shape(ms)
			}
		}
	}
	w := watchState(t, e.cli, WatchParams{})
	if got, w := read(w, func() { e.task("after", "quick") }), want("state-snapshot", 2); got != w {
		t.Fatalf("from nothing: %s, the web client expects %s", got, w)
	}
	w = watchState(t, e.cli, WatchParams{NoBriefs: true})
	var f StateFold
	w.fold(&f, e.c.State().Seq)
	for _, tk := range f.St.Tasks {
		if tk.Brief != "" {
			t.Fatalf("no_briefs: %+v", tk)
		}
	}
	w = watchState(t, e.cli, WatchParams{AfterSeq: 5, NoBriefs: true})
	if got, w := read(w, nil), want("state-resume", 7); got != w {
		t.Fatalf("after_seq: %s, the web client expects %s", got, w)
	}
}

func TestASnapshotComesInBatchesAndMatchesTheState(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	brief := strings.Repeat("b", maxBrief)
	for range 8 {
		var tk task.Task
		e.must(MTaskCreate, TaskCreate{Title: "big", Dir: t.TempDir(), Agent: "quick", Brief: brief}, &tk)
	}
	w := watchState(t, e.cli, WatchParams{})
	var f StateFold
	var batches int
	for f.St == nil {
		p := w.next()
		if p.Method == PushSnapshot {
			if len(p.Params) > snapshotBatch+maxBrief+4<<10 {
				t.Fatalf("a batch of %d bytes", len(p.Params))
			}
			var sn Snapshot
			p.Decode(&sn)
			if sn.Part == task.PartTasks {
				batches++
			}
		}
		if _, err := f.Apply(p); err != nil {
			t.Fatal(err)
		}
	}
	if batches < 2 {
		t.Fatalf("8 tasks of 256 KiB in %d batch", batches)
	}
	var st task.State
	e.must(MStateGet, nil, &st)
	a, _ := json.Marshal(f.St)
	b, _ := json.Marshal(st)
	if !bytes.Equal(a, b) {
		t.Fatal("the folded snapshot is not the state")
	}
}

func TestFromSnapshotToLiveNothingIsLostOrRepeated(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var busy atomic.Bool
	busy.Store(true)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for busy.Load() {
			var tk task.Task
			if e.call(MTaskCreate, TaskCreate{Title: "w", Dir: os.TempDir(), Agent: "quick"}, &tk) != nil {
				return
			}
		}
	}()
	var folds []*watched
	for range 5 {
		folds = append(folds, watchState(t, e.cli, WatchParams{}))
		time.Sleep(10 * time.Millisecond)
	}
	busy.Store(false)
	<-stopped
	end := e.c.State().Seq
	for i, w := range folds {
		var f StateFold
		seen := map[int64]bool{}
		for f.St == nil || f.St.Seq < end {
			p := w.next()
			if p.Method == PushJournal {
				var env journal.Envelope
				p.Decode(&env)
				if seen[env.Seq] {
					t.Fatalf("watch %d: seq %d twice", i, env.Seq)
				}
				seen[env.Seq] = true
			}
			if _, err := f.Apply(p); err != nil {
				t.Fatalf("watch %d: %v", i, err)
			}
		}
		if len(f.St.Tasks) != len(e.c.State().Tasks) {
			t.Fatalf("watch %d: %d tasks, the state has %d", i, len(f.St.Tasks), len(e.c.State().Tasks))
		}
	}
}

func TestResumingReplaysWhatCameAfter(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	e.task("a", "quick")
	e.task("b", "quick")
	e.task("c", "quick")
	w := watchState(t, e.cli, WatchParams{AfterSeq: 1})
	var o wire.Open
	if p := w.next(); p.Method != wire.PushOpen || p.Decode(&o) != nil || o.Mode != wire.ModeResume {
		t.Fatalf("%s %s", p.Method, p.Params)
	}
	if a, b := w.journal(), w.journal(); a.Seq != 2 || b.Seq != 3 {
		t.Fatal(a.Seq, b.Seq)
	}
	e.task("d", "quick")
	if env := w.journal(); env.Seq != 4 {
		t.Fatal(env.Seq)
	}
	for _, after := range []int64{9, 0} { // not this journal's, or no copy at all
		if p := watchState(t, e.cli, WatchParams{AfterSeq: after}).next(); p.Decode(&o) != nil || o.Mode != wire.ModeSnapshot {
			t.Fatalf("after %d: %s", after, p.Params)
		}
	}
}

// TestWhatAViewerMaySeeChangingResetsTheirCopy: the envelope that changed it reaches them only inside a new snapshot,
// and a copy made before it resumes as a snapshot, whether the stream was cut before or after that envelope.
func TestWhatAViewerMaySeeChangingResetsTheirCopy(t *testing.T) {
	e := team(t, tend.Config{})
	e.start()
	e.project()
	tk := e.taskAs(bob, "b1", "p1", "quick")
	before := e.c.State().Seq
	w := watchState(t, e.as(dee), WatchParams{AfterSeq: before})
	var f StateFold
	f.St = task.New()
	f.St.Seq = before
	if _, err := f.Apply(w.next()); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MProjectMember, "m-dee", task.MemberSet{Project: "p1", User: dee.User}, nil); err != nil {
		t.Fatal(err)
	}
	var got []wire.Push
	for {
		p := w.next()
		got = append(got, p)
		if p.Method == PushJournal {
			t.Fatalf("the envelope that changed what dee sees came as a journal push: %s", methods(got))
		}
		if _, err := f.Apply(p); err != nil {
			t.Fatal(err)
		}
		if p.Method == PushLive {
			break
		}
	}
	if got[0].Method != PushReset || f.St.Tasks[tk.ID] != nil {
		t.Fatalf("%s; dee still holds %v", methods(got), f.St.Tasks[tk.ID] != nil)
	}
	var o wire.Open
	if p := watchState(t, e.as(dee), WatchParams{AfterSeq: before}).next(); p.Decode(&o) != nil || o.Mode != wire.ModeSnapshot {
		t.Fatalf("a copy from before the change resumes as %s", p.Params)
	}
}

func TestAStateWatchEndsWhenTheCoordinatorCloses(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	w := watchState(t, e.cli, WatchParams{})
	var f StateFold
	w.fold(&f, 0)
	e.c.mu.Lock()
	for s, sb := range e.c.subs {
		delete(e.c.subs, s)
		sb.closed = true
		close(sb.ch)
	}
	e.c.mu.Unlock()
	select {
	case <-w.w.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still open")
	}
	if _, err := w.w.Next(context.Background()); wire.Code(err) != wire.CodeGone || errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

// TestACopyFollowsAcrossAChangeOfCoordinator: in mode 1 another process takes the lock; the copy resumes there.
func TestACopyFollowsAcrossAChangeOfCoordinator(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	e.task("a", "quick")
	w := watchState(t, e.cli, WatchParams{})
	var f StateFold
	w.fold(&f, 1)
	e.stop()
	e.start()
	e.task("b", "quick")
	w = watchState(t, e.cli, f.Params(false))
	var o wire.Open
	if p := w.next(); p.Decode(&o) != nil || o.Mode != wire.ModeResume {
		t.Fatalf("%s", p.Params)
	}
	w.fold(&f, 2)
	if len(f.St.Tasks) != 2 || !slices.ContainsFunc(mapsValues(f.St.Tasks), func(t *task.Task) bool { return t.Title == "b" }) {
		t.Fatalf("%+v", f.St.Tasks)
	}
}

func mapsValues[V any](m map[string]V) []V {
	var out []V
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

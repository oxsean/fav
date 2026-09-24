package coord

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

// TestMain doubles as tend: the supervisor and the fake agent are this binary started with _run / _fake-agent.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "_run" {
		if err := node.Supervise(os.Args[2]); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "_fake-agent" {
		if err := node.FakeAgent(os.Args[2:]); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	testkit.Main(m)
}

type env struct {
	t      *testing.T
	home   string
	cfg    fav.Config
	dial   func(h fav.Host, opt wire.Options) (Conn, error)
	c      *Coord
	cancel context.CancelFunc
	cli    *wire.Conn
	cmd    atomic.Int64
}

func newEnv(t *testing.T, cfg fav.Config) *env {
	e := &env{t: t, home: t.TempDir(), cfg: cfg}
	cfg.Agents = append(cfg.Agents,
		fav.AgentProfile{Name: "quick", Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "50ms"}},
		fav.AgentProfile{Name: "slow", Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "50ms", "--ask"}})
	e.cfg = cfg
	t.Cleanup(e.stop)
	return e
}

func (e *env) start() {
	e.t.Helper()
	c, err := Open(Options{Home: e.home, Version: "test", Config: e.cfg, Node: node.New(e.home), Sessions: remote.NewLocal("test"), Dial: e.dial})
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.c, e.cancel = c, cancel
	go c.Run(ctx)
	e.cli, _ = wire.Pipe(wire.Options{}, wire.Options{Handler: c.Handler()})
}

func (e *env) stop() {
	if e.c != nil {
		e.cancel()
		e.cli.Close()
		e.c.Close()
		e.c = nil
	}
}

func (e *env) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return e.cli.CallCommand(ctx, method, "cmd-"+time.Now().Format("150405")+"-"+itoa(e.cmd.Add(1)), params, out)
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func (e *env) must(method string, params, out any) {
	e.t.Helper()
	if err := e.call(method, params, out); err != nil {
		e.t.Fatalf("%s: %v", method, err)
	}
}

func (e *env) task(title, agentName string) *task.Task {
	e.t.Helper()
	return e.taskIn(title, agentName, e.t.TempDir())
}

func (e *env) taskIn(title, agentName, dir string) *task.Task {
	e.t.Helper()
	var t task.Task
	e.must(MTaskCreate, TaskCreate{Title: title, Dir: dir, Agent: agentName}, &t)
	return &t
}

func (e *env) dispatch(p Dispatch) *task.Run {
	e.t.Helper()
	if p.Runner == "" {
		p.Runner = node.RunnerBackground
	}
	var r task.Run
	e.must(MRunDispatch, p, &r)
	return &r
}

func (e *env) wait(id string, done func(*task.Run) bool) *task.Run {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r := e.c.State().Runs[id]
		if r != nil && done(r) {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("run %s: %+v", id, r)
		}
		e.c.poke()
		time.Sleep(50 * time.Millisecond)
	}
}

func ended(r *task.Run) bool { return !task.Open(r.State) }

func state(s string) func(*task.Run) bool { return func(r *task.Run) bool { return r.State == s } }

func TestATaskRunsOnThisMachineToItsEnd(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	tk := e.task("fix the build", "quick")
	r := e.dispatch(Dispatch{Task: tk.ID})
	if r.State != task.Queued || r.Machine != Local || r.Brief != "fix the build" {
		t.Fatalf("%+v", r)
	}
	end := e.wait(r.ID, ended)
	if end.State != task.Exited || end.ExitCode == nil || *end.ExitCode != 0 || end.Session == "" || end.StartedAt == nil {
		t.Fatalf("%+v", end)
	}
	var again task.Run
	if err := e.call(MRunDispatch, Dispatch{Task: tk.ID, Runner: node.RunnerBackground}, &again); err != nil || again.ID == r.ID {
		t.Fatalf("a task runs again once its run ended: %+v %v", again, err)
	}
}

func TestACommandIDAnswersItsFirstResult(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	ctx := context.Background()
	var a, b task.Task
	p := TaskCreate{Title: "one", Dir: t.TempDir()}
	if err := e.cli.CallCommand(ctx, MTaskCreate, "k1", p, &a); err != nil {
		t.Fatal(err)
	}
	if err := e.cli.CallCommand(ctx, MTaskCreate, "k1", p, &b); err != nil || a.ID != b.ID {
		t.Fatalf("replay made another task: %s %s %v", a.ID, b.ID, err)
	}
	if err := e.cli.CallCommand(ctx, MTaskCreate, "k1", TaskCreate{Title: "two"}, &b); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("same id, other params: %v", err)
	}
	if err := e.cli.Call(ctx, MTaskCreate, p, &b); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("no command id: %v", err)
	}
	if n := len(e.c.State().Tasks); n != 1 {
		t.Fatalf("%d tasks", n)
	}
}

func TestAnOpenRunHoldsItsTask(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	tk := e.task("x", "slow")
	r := e.dispatch(Dispatch{Task: tk.ID})
	if err := e.call(MRunDispatch, Dispatch{Task: tk.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("second run of an open task: %v", err)
	}
	e.wait(r.ID, state(task.Running))
	if err := e.call(MRunAbandon, task.RunRef{ID: r.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("abandoning a running run: %v", err)
	}
	e.must(MRunStop, task.RunRef{ID: r.ID}, nil)
	end := e.wait(r.ID, ended)
	if end.State != task.Stopped || end.Reason != "asked" {
		t.Fatalf("%+v", end)
	}
}

func TestSlotsAndDirectoriesQueueRuns(t *testing.T) {
	e := newEnv(t, fav.Config{Machines: map[string]fav.MachineConfig{Local: {Slots: 2}}})
	e.start()
	shared := t.TempDir()
	a := e.dispatch(Dispatch{Task: e.taskIn("a", "slow", shared).ID})
	b := e.dispatch(Dispatch{Task: e.taskIn("b", "slow", shared).ID})
	c := e.dispatch(Dispatch{Task: e.task("c", "slow").ID})
	d := e.dispatch(Dispatch{Task: e.task("d", "slow").ID})
	e.wait(a.ID, state(task.Running))
	e.wait(c.ID, state(task.Running))
	time.Sleep(300 * time.Millisecond)
	st := e.c.State()
	if st.Runs[b.ID].State != task.Queued || st.Runs[d.ID].State != task.Queued {
		t.Fatalf("b (same dir) %s, d (no slot) %s", st.Runs[b.ID].State, st.Runs[d.ID].State)
	}
	e.must(MRunStop, task.RunRef{ID: d.ID}, nil)
	if got := e.c.State().Runs[d.ID].State; got != task.Canceled {
		t.Fatalf("stopping a queued run: %s", got)
	}
	e.must(MRunStop, task.RunRef{ID: a.ID}, nil)
	e.wait(b.ID, state(task.Running))
	for _, id := range []string{b.ID, c.ID} {
		e.must(MRunStop, task.RunRef{ID: id}, nil)
		e.wait(id, ended)
	}
}

func TestRunsGoOnWithoutTheCoordinator(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID})
	e.wait(r.ID, state(task.Running))
	e.stop()
	n := node.New(e.home)
	if _, err := n.Stop(r.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for s, _ := n.Snapshot(r.ID); !node.Terminal(s.State.State); s, _ = n.Snapshot(r.ID) {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.start()
	if got := e.c.State().Runs[r.ID]; got.State != task.Running {
		t.Fatalf("the journal lost the run: %+v", got)
	}
	end := e.wait(r.ID, ended)
	if end.State != task.Stopped {
		t.Fatalf("%+v", end)
	}
}

// far is another machine: a node in its own home, reached through a pipe that tests can break.
type far struct {
	mu    sync.Mutex
	home  string
	fail  error
	conns []*wire.Conn
	dials int
}

func (f *far) dial(h fav.Host, opt wire.Options) (Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials++
	if f.fail != nil {
		return nil, f.fail
	}
	n := node.New(f.home)
	a, b := wire.Pipe(opt, wire.Options{Handler: n.Handler(remote.NewLocal("far"))})
	go n.Watch(b.Done(), func(runs []string) { b.Push(node.MChanged, node.Changed{Runs: runs}) })
	f.conns = append(f.conns, b)
	return a, nil
}

func (f *far) set(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

func (f *far) cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
}

func TestAMachineThatCannotBeReachedKeepsItsRunsQueued(t *testing.T) {
	f := &far{home: t.TempDir()}
	f.set(&wire.Error{Code: wire.CodeOffline, Detail: "no route"})
	e := newEnv(t, fav.Config{Hosts: []fav.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID, Machine: "far"})
	var ms Machines
	deadline := time.Now().Add(10 * time.Second)
	for {
		e.must(MMachineList, MachinesParams{}, &ms)
		if m := ms.Machines[1]; m.State == MachineOffline && m.Error == wire.CodeOffline && m.RetryAt != nil && m.Queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", ms)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := e.c.State().Runs[r.ID].State; got != task.Queued {
		t.Fatalf("%s", got)
	}
	f.set(nil)
	e.must(MMachineList, MachinesParams{Connect: true}, &ms)
	end := e.wait(r.ID, ended)
	if end.State != task.Exited {
		t.Fatalf("%+v", end)
	}
	if _, err := os.Stat(filepath.Join(f.home, "node", "runs", r.ID)); err != nil {
		t.Fatalf("the run did not happen on far: %v", err)
	}
}

func TestALostConnectionIsReconciledOnReconnect(t *testing.T) {
	f := &far{home: t.TempDir()}
	e := newEnv(t, fav.Config{Hosts: []fav.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID, Machine: "far"})
	e.wait(r.ID, state(task.Running))
	f.cut()
	if _, err := node.New(f.home).Stop(r.ID); err != nil {
		t.Fatal(err)
	}
	e.c.mu.Lock()
	e.c.ms["far"].retryAt = time.Time{}
	e.c.mu.Unlock()
	end := e.wait(r.ID, func(r *task.Run) bool {
		e.c.mu.Lock()
		e.c.ms["far"].retryAt = time.Time{} // skip the backoff
		e.c.mu.Unlock()
		return ended(r)
	})
	if end.State != task.Stopped {
		t.Fatalf("%+v", end)
	}
	if f.dials < 2 {
		t.Fatalf("dialed %d times", f.dials)
	}
}

func TestAStopAskedWhileOfflineIsDeliveredLater(t *testing.T) {
	f := &far{home: t.TempDir()}
	e := newEnv(t, fav.Config{Hosts: []fav.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID, Machine: "far"})
	e.wait(r.ID, state(task.Running))
	f.set(errors.New("down"))
	f.cut()
	e.must(MRunStop, task.RunRef{ID: r.ID}, nil)
	time.Sleep(300 * time.Millisecond)
	if got := e.c.State().Runs[r.ID]; got.State != task.Running || got.Want != "stop" {
		t.Fatalf("%+v", got)
	}
	f.set(nil)
	end := e.wait(r.ID, func(r *task.Run) bool {
		e.c.mu.Lock()
		e.c.ms["far"].retryAt = time.Time{}
		e.c.mu.Unlock()
		return ended(r)
	})
	if end.State != task.Stopped {
		t.Fatalf("%+v", end)
	}
}

func TestANodeThatRefusesARunFailsIt(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	e.c.opt.Node.Limits = fav.NodeConfig{AllowProfiles: []string{"slow"}}
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	end := e.wait(r.ID, ended)
	if end.State != task.Failed || end.Reason == "" {
		t.Fatalf("%+v", end)
	}
}

func TestSubscribersGetEveryEnvelopeInOrder(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	e.task("before", "quick")
	var mu sync.Mutex
	var seqs []int64
	cli, _ := wire.Pipe(wire.Options{OnPush: func(method string, params json.RawMessage) {
		var env journal.Envelope
		json.Unmarshal(params, &env)
		mu.Lock()
		seqs = append(seqs, env.Seq)
		mu.Unlock()
	}}, wire.Options{Handler: e.c.Handler()})
	defer cli.Close()
	var s Subscribed
	if err := cli.Call(context.Background(), MSubscribe, SubscribeParams{}, &s); err != nil || s.Seq != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	for i := 0; i < 5; i++ {
		e.task("after", "quick")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]int64(nil), seqs...)
		mu.Unlock()
		if len(got) == 6 {
			for i, q := range got {
				if q != int64(i+1) {
					t.Fatalf("%v", got)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOneCoordinatorAtATime(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	if _, err := Open(Options{Home: e.home, Node: node.New(e.home)}); !errors.Is(err, ErrLocked) {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.c.Serve(ctx)
	var cl *Client
	var err error
	for i := 0; i < 50; i++ {
		if cl, err = Connect(Options{Home: e.home}, wire.Options{}); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || cl.Coord != nil {
		t.Fatalf("a second client became a coordinator: %v", err)
	}
	defer cl.Close()
	var st task.State
	if err := cl.Call(context.Background(), MStateGet, nil, &st); err != nil || st.Seq != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestAStartThatNeverReachedTheNodeIsSentAgain(t *testing.T) {
	e := newEnv(t, fav.Config{})
	e.start()
	tk := e.task("x", "quick")
	e.stop()
	c, err := Open(Options{Home: e.home, Config: e.cfg, Node: node.New(e.home)})
	if err != nil {
		t.Fatal(err)
	}
	prof, _ := c.profile("quick")
	run := task.Run{ID: node.NewRunID(), Task: tk.ID, Machine: Local, Agent: "quick", Profile: prof, Dir: tk.Dir, Brief: "x",
		Runner: node.RunnerBackground}
	c.mu.Lock()
	err = c.commit(nil, journal.NewEvent(task.ERunQueued, run), journal.NewEvent(task.ERunStarting, task.RunStarting{ID: run.ID, Dir: run.Dir}))
	c.mu.Unlock()
	c.Close()
	if err != nil {
		t.Fatal(err)
	}
	e.start()
	if end := e.wait(run.ID, ended); end.State != task.Exited {
		t.Fatalf("%+v", end)
	}
}

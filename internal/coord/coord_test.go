package coord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/proc"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/testkit"
	"github.com/oxsean/fav/internal/wire"
)

// TestMain doubles as tend: the supervisor and the fake agent are this binary started with _run / _fake-agent.
func TestMain(m *testing.M) {
	if os.Getenv("TEND_TEST_LOG") == "sqlite" {
		defaultLog = sqliteLog
	}
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
	if len(os.Args) > 2 && os.Args[1] == "_check" { // a check hook: fails the first time, passes after
		if _, err := os.Stat(os.Args[2]); err != nil {
			os.WriteFile(os.Args[2], nil, 0o600)
			fmt.Println("FAIL: TestExport (quotes)")
			os.Exit(1)
		}
		fmt.Println("ok")
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "_notify" { // a notify command: keeps what it read
		b, _ := io.ReadAll(os.Stdin)
		f, _ := os.OpenFile(os.Args[2], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		f.Write(b)
		f.Close()
		os.Exit(0)
	}
	testkit.Main(m)
}

type env struct {
	t      *testing.T
	home   string
	cfg    tend.Config
	dial   func(h tend.Host, opt wire.Options) (Conn, error)
	c      *Coord
	cancel context.CancelFunc
	cli    *wire.Conn
	cmd    atomic.Int64
	probe  func(provider string) agent.Check
	owner  func(machine string) string // team mode
	users  map[string]User
	usersM sync.Mutex
	notice func(Notice)
}

func newEnv(t *testing.T, cfg tend.Config) *env {
	e := &env{t: t, home: t.TempDir(), cfg: cfg}
	cfg.Agents = append(cfg.Agents,
		tend.AgentProfile{Name: "quick", Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "50ms"}},
		tend.AgentProfile{Name: "slow", Provider: agent.ProviderFake, Args: []string{"--steps", "1", "--every", "50ms", "--ask"}})
	e.cfg = cfg
	t.Cleanup(e.stop)
	t.Cleanup(func() { killRuns(e.home) })
	return e
}

// killRuns ends every supervisor and agent a test left running (a failed test would leave --ask agents for good).
func killRuns(home string) {
	dirs, _ := filepath.Glob(filepath.Join(home, "node", "runs", "*"))
	var killed []int
	for _, d := range dirs {
		var st node.State
		if b, err := os.ReadFile(filepath.Join(d, "state.json")); err == nil && json.Unmarshal(b, &st) == nil {
			for _, pid := range []int{st.Pid, st.Sup} {
				if pid > 0 {
					proc.KillPID(pid)
					killed = append(killed, pid)
				}
			}
		}
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) { // ⚠️ Windows keeps their files until they are gone
		if !slices.ContainsFunc(killed, proc.Alive) {
			return
		}
	}
}

func (e *env) start() {
	e.t.Helper()
	n := node.New(e.home)
	n.Probe = e.probe
	n.Limits.AllowHooks = e.cfg.Node.AllowHooks
	opt := Options{Home: e.home, Version: "test", Config: e.cfg, Node: n, Sessions: remote.NewLocal("test"), Dial: e.dial, MachineOwner: e.owner,
		Notice: e.notice}
	if e.users != nil {
		opt.Users = func(id string) (User, bool) {
			e.usersM.Lock()
			defer e.usersM.Unlock()
			u, ok := e.users[id]
			return u, ok
		}
	}
	c, err := Open(opt)
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
	e := newEnv(t, tend.Config{})
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
	e := newEnv(t, tend.Config{})
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
	e := newEnv(t, tend.Config{})
	e.start()
	tk := e.task("x", "slow")
	r := e.dispatch(Dispatch{Task: tk.ID})
	if err := e.call(MRunDispatch, Dispatch{Task: tk.ID}, nil); wire.Code(err) != wire.CodeConflict {
		t.Fatalf("second run of an open task: %v", err)
	}
	e.wait(r.ID, state(task.Running))
	e.must(MRunStop, task.RunRef{ID: r.ID}, nil)
	end := e.wait(r.ID, ended)
	if end.State != task.Stopped || end.Reason != "asked" {
		t.Fatalf("%+v", end)
	}
}

func TestSlotsAndDirectoriesQueueRuns(t *testing.T) {
	e := newEnv(t, tend.Config{Machines: map[string]tend.MachineConfig{Local: {Slots: 2}}})
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
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID})
	e.wait(r.ID, state(task.Running))
	e.stop()
	n := node.New(e.home)
	if _, err := n.Stop(node.RunRef{Run: r.ID}); err != nil {
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
	hide  string // a run run.list leaves out, as when its snapshot cannot be read
}

func newFar(t *testing.T) *far {
	f := &far{home: t.TempDir()}
	t.Cleanup(func() { killRuns(f.home) })
	return f
}

func (f *far) dial(h tend.Host, opt wire.Options) (Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials++
	if f.fail != nil {
		return nil, f.fail
	}
	n := node.New(f.home)
	handle := n.Handler(remote.NewLocal("far"))
	a, b := wire.Pipe(opt, wire.Options{Handler: func(ctx context.Context, r *wire.Request) (any, error) {
		res, err := handle(ctx, r)
		f.mu.Lock()
		hide := f.hide
		f.mu.Unlock()
		if runs, ok := res.(node.Runs); ok && hide != "" {
			runs.Runs = slices.DeleteFunc(slices.Clone(runs.Runs), func(s node.Snapshot) bool { return s.Run == hide })
			return runs, err
		}
		return res, err
	}})
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
	f := newFar(t)
	f.set(&wire.Error{Code: wire.CodeOffline, Detail: "no route"})
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
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
	f := newFar(t)
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID, Machine: "far"})
	e.wait(r.ID, state(task.Running))
	f.cut()
	if _, err := node.New(f.home).Stop(node.RunRef{Run: r.ID}); err != nil {
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
	f := newFar(t)
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
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
	e := newEnv(t, tend.Config{})
	e.start()
	e.c.opt.Node.Limits = tend.NodeConfig{AllowProfiles: []string{"slow"}}
	r := e.dispatch(Dispatch{Task: e.task("x", "quick").ID})
	end := e.wait(r.ID, ended)
	if end.State != task.Failed || end.Reason == "" {
		t.Fatalf("%+v", end)
	}
}

func TestSubscribersGetEveryEnvelopeInOrder(t *testing.T) {
	e := newEnv(t, tend.Config{})
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
	e := newEnv(t, tend.Config{})
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
	e := newEnv(t, tend.Config{})
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
	err = c.commit(journal.System, nil, journal.NewEvent(task.ERunQueued, run), journal.NewEvent(task.ERunStarting, task.RunStarting{ID: run.ID, Dir: run.Dir}))
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

func TestAnAbandonedRunIsAskedToStop(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	tk := e.task("x", "slow")
	r := e.dispatch(Dispatch{Task: tk.ID})
	e.wait(r.ID, state(task.Running))
	if err := e.call(MRunAbandon, task.RunRef{ID: r.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.c.State().Runs[r.ID].State; got != task.Abandoned {
		t.Fatal(got)
	}
	if end := e.wait(r.ID, state(task.Stopped)); end.Reason != "asked" {
		t.Fatalf("%+v", end)
	}
}

func TestARunGoneFromItsNodeCanBeGivenUp(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	r := e.dispatch(Dispatch{Task: e.task("x", "slow").ID})
	e.wait(r.ID, state(task.Running))
	killRuns(e.home)
	dir := filepath.Join(e.home, "node", "runs", r.ID)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if os.RemoveAll(dir) == nil { // ⚠️ Windows: a killed process's files close a little later
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run directory stays")
		}
	}
	if got := e.wait(r.ID, state(task.Unknown)); got.Reason != "missing" {
		t.Fatalf("%+v", got)
	}
	e.must(MRunAbandon, task.RunRef{ID: r.ID}, nil)
	if got := e.c.State().Runs[r.ID].State; got != task.Abandoned {
		t.Fatal(got)
	}
}

func TestAStopOfAStartThatNeverArrivedEndsTheRun(t *testing.T) {
	e := newEnv(t, tend.Config{})
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
	err = c.commit(journal.System, nil, journal.NewEvent(task.ERunQueued, run), journal.NewEvent(task.ERunStarting, task.RunStarting{ID: run.ID, Dir: run.Dir}),
		journal.NewEvent(task.ERunStopAsked, task.RunRef{ID: run.ID}))
	c.mu.Unlock()
	c.Close()
	if err != nil {
		t.Fatal(err)
	}
	e.start()
	if end := e.wait(run.ID, ended); end.State != task.Stopped || end.Reason != "never_started" {
		t.Fatalf("%+v", end)
	}
}

func TestADirectoryIsMappedFromTheMachineItWasWrittenFor(t *testing.T) {
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	c, err := Open(Options{Home: e.home, Config: e.cfg, Node: node.New(e.home), Sessions: remote.NewLocal("test")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	home, _ := os.UserHomeDir()
	c.ms["far"].hello = remote.Hello{OS: "linux", Home: "/root"}
	r := &task.Run{Task: "t_1", Dir: filepath.Join(home, "dev", "tend"), From: Local}
	if got, ok := c.mapDir(r, c.ms["far"]); !ok || got != "/root/dev/tend" {
		t.Fatalf("this machine never connected: %s", got)
	}
}

func TestADirectoryIsBusyWhateverItsCase(t *testing.T) {
	if dirKey(`C:\Work\A`, "windows") != dirKey(`c:/work/a/`, "windows") || dirKey("/w/A", "linux") == dirKey("/w/a", "linux") {
		t.Fatal("dirKey")
	}
}

func TestAReplayedCommandAnswersWhatItAnsweredFirst(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	ctx := context.Background()
	var a, b task.Task
	if err := e.cli.CallCommand(ctx, MTaskCreate, "k1", TaskCreate{Title: "one", Dir: t.TempDir()}, &a); err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	e.must(MTaskEdit, task.TaskEdit{ID: a.ID, Title: &title}, nil)
	if err := e.cli.CallCommand(ctx, MTaskCreate, "k1", TaskCreate{Title: "one", Dir: a.Dir}, &b); err != nil || b.Title != "one" || b.Rev != 1 {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestStateWithoutBriefsAndOneTask(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var tk task.Task
	e.must(MTaskCreate, TaskCreate{Title: "x", Brief: "the brief", Dir: t.TempDir()}, &tk)
	if err := e.call(MTaskCreate, TaskCreate{Title: "big", Brief: strings.Repeat("x", maxBrief+1)}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a brief over the limit: %v", err)
	}
	var st task.State
	if err := e.cli.Call(context.Background(), MStateGet, StateParams{NoBriefs: true}, &st); err != nil || st.Tasks[tk.ID].Brief != "" {
		t.Fatalf("%+v %v", st.Tasks[tk.ID], err)
	}
	var got task.Task
	if err := e.cli.Call(context.Background(), MTaskGet, task.RunRef{ID: tk.ID}, &got); err != nil || got.Brief != "the brief" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := e.cli.Call(context.Background(), MTaskGet, task.RunRef{ID: "t_nope"}, &got); wire.Code(err) != wire.CodeNotFound {
		t.Fatal(err)
	}
}

func TestACallWaitsOutTheBackoff(t *testing.T) {
	f := newFar(t)
	f.set(&wire.Error{Code: wire.CodeOffline})
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	var ms Machines
	e.must(MMachineList, MachinesParams{Connect: true}, &ms)
	f.mu.Lock()
	dials := f.dials
	f.mu.Unlock()
	for range 3 {
		var out json.RawMessage
		if err := e.cli.Call(context.Background(), MNodeCall, NodeCall{Machine: "far", Method: remote.MHello}, &out); wire.Code(err) != wire.CodeOffline {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dials != dials {
		t.Fatalf("calls dialed %d more times while backing off", f.dials-dials)
	}
}

func TestAnAbandonedStartThatLandsLateIsStopped(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	tk := e.task("x", "slow")
	e.stop()
	c, err := Open(Options{Home: e.home, Config: e.cfg, Node: node.New(e.home), Sessions: remote.NewLocal("test")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	prof, _ := c.profile("slow")
	run := task.Run{ID: node.NewRunID(), Task: tk.ID, Machine: Local, Agent: "slow", Profile: prof, Dir: tk.Dir, Brief: "x",
		Runner: node.RunnerBackground}
	c.mu.Lock()
	err = c.commit(journal.System, nil, journal.NewEvent(task.ERunQueued, run), journal.NewEvent(task.ERunStarting, task.RunStarting{ID: run.ID, Dir: run.Dir}),
		journal.NewEvent(task.ERunAbandoned, task.RunRef{ID: run.ID}))
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		c.Pass(context.Background())
		c.waitDials(context.Background())
	}
	n := node.New(e.home)
	s, err := n.Start(node.StartParams{Run: run.ID, Task: tk.ID, Coordinator: c.id, Profile: prof, Dir: tk.Dir, Brief: "x", Runner: node.RunnerBackground})
	if err != nil || s.State.State != node.StateStopped || s.Reason != "never_started" {
		t.Fatalf("the late start finds the tombstone: %+v %v", s, err)
	}
	if got := c.State().Runs[run.ID]; got.State != task.Stopped {
		t.Fatalf("%+v", got)
	}
}

func TestAnAbandonedRunKeepsItsDirectoryUntilItStops(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	dir := t.TempDir()
	a := e.dispatch(Dispatch{Task: e.taskIn("a", "slow", dir).ID})
	e.wait(a.ID, state(task.Running))
	e.must(MRunAbandon, task.RunRef{ID: a.ID}, nil)
	b := e.dispatch(Dispatch{Task: e.taskIn("b", "slow", dir).ID})
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		st := e.c.State()
		if st.Runs[a.ID].State == task.Stopped {
			break
		}
		if st.Runs[b.ID].State != task.Queued {
			t.Fatalf("b started while a still ran in its directory: a %s, b %s", st.Runs[a.ID].State, st.Runs[b.ID].State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%+v", st.Runs[a.ID])
		}
		e.c.poke()
	}
	e.wait(b.ID, state(task.Running))
	e.must(MRunStop, task.RunRef{ID: b.ID}, nil)
	e.wait(b.ID, ended)
}

func TestAnAbandonedRunTheNodeDoesNotListKeepsItsDirectory(t *testing.T) {
	f := newFar(t)
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	dir := t.TempDir()
	a := e.dispatch(Dispatch{Task: e.taskIn("a", "slow", dir).ID, Machine: "far"})
	e.wait(a.ID, state(task.Running))
	f.mu.Lock()
	f.hide = a.ID
	f.mu.Unlock()
	e.must(MRunAbandon, task.RunRef{ID: a.ID}, nil)
	b := e.dispatch(Dispatch{Task: e.taskIn("b", "slow", dir).ID, Machine: "far"})
	for range 20 {
		e.c.poke()
		time.Sleep(100 * time.Millisecond)
		if st := e.c.State(); st.Runs[b.ID].State != task.Queued {
			t.Fatalf("b started while a may still run in its directory: a %s, b %s", st.Runs[a.ID].State, st.Runs[b.ID].State)
		}
	}
	f.mu.Lock()
	f.hide = ""
	f.mu.Unlock()
	e.wait(b.ID, state(task.Running))
	e.must(MRunStop, task.RunRef{ID: b.ID}, nil)
	e.wait(b.ID, ended)
}

func TestARunGoneFromAFarNodeIsNoticedWhileOthersRun(t *testing.T) {
	f := newFar(t)
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = f.dial
	e.start()
	here := e.dispatch(Dispatch{Task: e.task("here", "slow").ID})
	there := e.dispatch(Dispatch{Task: e.task("there", "slow").ID, Machine: "far"})
	e.wait(here.ID, state(task.Running))
	e.wait(there.ID, state(task.Running))
	killRuns(f.home)
	dir := filepath.Join(f.home, "node", "runs", there.ID)
	for deadline := time.Now().Add(10 * time.Second); os.RemoveAll(dir) != nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the run directory stays")
		}
	}
	if got := e.wait(there.ID, state(task.Unknown)); got.Reason != "missing" {
		t.Fatalf("%+v", got)
	}
	e.must(MRunStop, task.RunRef{ID: here.ID}, nil)
	e.wait(here.ID, ended)
}

func TestAReadOnlyCommandLeavesAtOnce(t *testing.T) {
	f := newFar(t)
	f.set(&wire.Error{Code: wire.CodeOffline})
	e := newEnv(t, tend.Config{Hosts: []tend.Host{{Name: "far", SSH: "far"}}})
	e.dial = func(h tend.Host, opt wire.Options) (Conn, error) {
		time.Sleep(3 * time.Second) // an ssh that never answers
		return f.dial(h, opt)
	}
	e.start()
	e.dispatch(Dispatch{Task: e.task("x", "quick").ID, Machine: "far"})
	e.stop()
	opt := Options{Home: e.home, Version: "test", Config: e.cfg, Node: node.New(e.home), Sessions: remote.NewLocal("test"), Dial: e.dial}
	cl, err := Connect(opt, wire.Options{})
	if err != nil || cl.Coord == nil {
		t.Fatal(err)
	}
	var st task.State
	if err := cl.Call(context.Background(), MStateGet, StateParams{NoBriefs: true}, &st); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	cl.Close()
	if d := time.Since(began); d > time.Second {
		t.Fatalf("a read-only command waited %s to leave", d)
	}
}

func TestADirectoryKeyFollowsTheTargetsRules(t *testing.T) {
	if dirKey(`C:\Work\A`, "windows") != dirKey(`c:/work/./b/../a//`, "windows") {
		t.Fatal("windows")
	}
}

func TestTitlesAreCappedAndHerdrRunsClaudeOnly(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	if err := e.call(MTaskCreate, TaskCreate{Title: strings.Repeat("x", maxTitle+1)}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatal(err)
	}
	tk := e.task("x", "codex")
	if err := e.call(MRunDispatch, Dispatch{Task: tk.ID, Runner: node.RunnerHerdr}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatal(err)
	}
}

func TestATaskKeepsItsProject(t *testing.T) {
	e := newEnv(t, tend.Config{})
	e.start()
	var tk task.Task
	e.must(MTaskCreate, TaskCreate{Title: "with a project", Dir: t.TempDir(), Project: " tend "}, &tk)
	if tk.Project != "tend" {
		t.Fatalf("created: %q", tk.Project)
	}
	other := "homelab"
	e.must(MTaskEdit, task.TaskEdit{ID: tk.ID, Project: &other}, &tk)
	if tk.Project != "homelab" || e.c.State().Tasks[tk.ID].Project != "homelab" {
		t.Fatalf("edited: %q", tk.Project)
	}
	long := strings.Repeat("p", maxProject+1)
	if err := e.call(MTaskEdit, task.TaskEdit{ID: tk.ID, Project: &long}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("a project name has a bound: %v", err)
	}
}

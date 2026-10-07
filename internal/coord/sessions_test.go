package coord

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/journal"
	"github.com/oxsean/fav/internal/migrate"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// queryOf is the query a node reads from p.
func queryOf(p remote.QueryParams) tend.Query {
	q := tend.Parse(p.Q)
	q.All, q.Host = p.All, tend.HostLocal
	if p.Also != nil {
		q.Also = func(r *tend.Rec) string { return p.Also[r.SessionID] }
	}
	return q
}

// belongs is Rows.Belong for the projects a query brings, on a machine running goos.
func belongs(projects []remote.ProjectDirs, goos string) func(*tend.Rec) (string, string) {
	prs := map[string]*task.Project{}
	for _, pd := range projects {
		pr := &task.Project{ID: pd.ID, Name: pd.Name}
		for _, d := range pd.Dirs {
			pr.Repos = append(pr.Repos, task.Repo{Dirs: map[string]string{"here": d}})
		}
		prs[pd.ID] = pr
	}
	return func(r *tend.Rec) (string, string) {
		if pr := task.ProjectOf(prs, "here", goos, cmp.Or(r.Repo, r.Cwd)); pr != nil {
			return pr.ID, pr.Name
		}
		return "", ""
	}
}

// answer is query's answer: recs placed by rows, selected and paged as p asks.
func answer(rows *index.Rows, recs []*tend.Rec, q tend.Query, p remote.QueryParams, live map[string]capture.Live) remote.QueryResult {
	order, _ := tend.ParseSort(p.Sort)
	sel := index.Select(rows, recs, q, index.Page{Sort: order, After: p.After, Limit: p.Limit})
	res := remote.QueryResult{Rows: []remote.Row{}, Next: sel.Next, Total: sel.Total, Matched: sel.Matched, Running: sel.Running,
		Facets: sel.Facets, Tokens: tend.Tokens(p.Q)}
	if q.Status == tend.StatusTrash || q.Status == tend.StatusAgent {
		res.Status = q.Status
	}
	for _, r := range sel.Rows {
		row := remote.Row{Session: remote.SessionOf(r), Project: r.ProjectID}
		if l, ok := live[r.SessionID]; ok {
			row.Live = new(l)
		}
		res.Rows = append(res.Rows, row)
	}
	return res
}

// fakeNode is a node of sessions it holds: it answers query, put, trash and restore as a node does, list and live as
// any does, grep with what it is given; old answers as a node that has no query or put, noTrash as one that has those
// and no trash or restore.
type fakeNode struct {
	mu        sync.Mutex
	sessions  []remote.Session
	trashed   []remote.Row // its trash, each row with deleted_at
	trashDays int
	live      map[string]capture.Live
	old       bool
	noTrash   bool
	resume    bool          // it has run.resume
	share     string        // share_sessions
	wait      time.Duration // query answers after this, or when the caller gives up
	refuse    string        // query answers this error
	grep      *remote.GrepResult
	asked     []remote.QueryParams
	puts      []remote.PutParams
	trashes   []string // method:session of each trash and restore
}

func (f *fakeNode) handle(ctx context.Context, r *wire.Request) (any, error) {
	f.mu.Lock()
	wait := f.wait
	f.mu.Unlock()
	if r.Method == remote.MQuery && wait > 0 {
		select {
		case <-ctx.Done():
			return nil, &wire.Error{Code: wire.CodeCanceled}
		case <-time.After(wait):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case remote.MHello:
		ms := []string{remote.MHello, remote.MList, remote.MLive, node.MRunStart, node.MRunList}
		if f.resume {
			ms = append(ms, node.MRunResume)
		}
		if !f.old {
			ms = append(ms, remote.MQuery, remote.MPut)
		}
		if !f.old && !f.noTrash {
			ms = append(ms, remote.MTrash, remote.MRestore)
		}
		if f.grep != nil {
			ms = append(ms, remote.MGrep)
		}
		return remote.Hello{Proto: wire.Proto, Version: "fake", OS: "linux", Methods: ms, Share: f.share}, nil
	case node.MRunList:
		return node.Runs{}, nil
	case remote.MList:
		return remote.List{Sessions: slices.Clone(f.sessions)}, nil
	case remote.MLive:
		return remote.Live{Live: f.live}, nil
	case remote.MGrep:
		if f.grep != nil {
			return *f.grep, nil
		}
	case remote.MQuery:
		if f.old {
			break
		}
		var p remote.QueryParams
		if err := r.Decode(&p); err != nil {
			return nil, err
		}
		f.asked = append(f.asked, p)
		if f.refuse != "" {
			return nil, &wire.Error{Code: f.refuse}
		}
		q := queryOf(p)
		q.Live = func(id string) bool { _, ok := f.live[id]; return ok }
		if q.Status == tend.StatusTrash {
			if f.noTrash {
				return remote.QueryResult{Rows: []remote.Row{}, Tokens: tend.Tokens(p.Q)}, nil
			}
			var recs []*tend.Rec
			deleted := map[string]*time.Time{}
			for _, r := range f.trashed {
				recs = append(recs, r.Rec(""))
				deleted[r.SessionID] = r.DeletedAt
			}
			q.Status, q.All, q.Turns = "all", true, 0
			res := answer(&index.Rows{Belong: belongs(p.Projects, "linux")}, recs, q, p, nil)
			for i := range res.Rows {
				res.Rows[i].DeletedAt = deleted[res.Rows[i].SessionID]
			}
			res.TrashDays = f.trashDays
			return res, nil
		}
		var recs []*tend.Rec
		for _, s := range f.sessions {
			recs = append(recs, s.Rec(""))
		}
		res := answer(&index.Rows{Belong: belongs(p.Projects, "linux")}, recs, q, p, f.live)
		res.TrashDays = f.trashDays
		return res, nil
	case remote.MTrash, remote.MRestore:
		if f.old || f.noTrash {
			break
		}
		var ref remote.Ref
		if err := r.Decode(&ref); err != nil {
			return nil, err
		}
		f.trashes = append(f.trashes, r.Method+":"+ref.SessionID)
		if r.Method == remote.MTrash {
			at := slices.IndexFunc(f.sessions, func(s remote.Session) bool { return s.SessionID == ref.SessionID })
			if at < 0 {
				return nil, &wire.Error{Code: wire.CodeNotFound, Detail: ref.SessionID}
			}
			s := f.sessions[at]
			f.sessions = slices.Delete(f.sessions, at, at+1)
			f.trashed = append(f.trashed, remote.Row{Session: s, DeletedAt: new(time.Now())})
			return remote.TrashResult{Title: s.Title, Files: 1}, nil
		}
		at := slices.IndexFunc(f.trashed, func(r remote.Row) bool { return r.SessionID == ref.SessionID })
		if at < 0 {
			return nil, &wire.Error{Code: wire.CodeNotFound, Detail: ref.SessionID}
		}
		row := f.trashed[at]
		f.trashed = slices.Delete(f.trashed, at, at+1)
		f.sessions = append(f.sessions, row.Session)
		return remote.RestoreResult{Title: row.Title, Files: 1}, nil
	case remote.MPut:
		if f.old {
			break
		}
		var p remote.PutParams
		if err := r.Decode(&p); err != nil {
			return nil, err
		}
		f.puts = append(f.puts, p)
		for i, s := range f.sessions {
			if s.Provider == p.Provider && s.SessionID == p.SessionID {
				rec := s.Rec("")
				p.Patch.Apply(rec, time.Now())
				f.sessions[i] = remote.SessionOf(rec)
				return remote.Row{Session: f.sessions[i]}, nil
			}
		}
		return nil, &wire.Error{Code: wire.CodeNotFound, Detail: p.SessionID}
	}
	return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: r.Method}
}

func (f *fakeNode) queries() []remote.QueryParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// attach connects f as machine name; the other end pushes as its node.
func (e *env) attach(name string, f *fakeNode) *wire.Conn {
	e.t.Helper()
	toNode, nodeEnd := wire.Pipe(e.c.NodeOptionsFor(name), wire.Options{Handler: f.handle})
	e.t.Cleanup(func() { nodeEnd.Close() })
	if err := e.c.Attach(name, toNode, nil); err != nil {
		e.t.Fatal(err)
	}
	return nodeEnd
}

// served is a mode 2 team env: ann owns every machine but those owners names.
func served(t *testing.T, owners map[string]string) *env {
	e := team(t, tend.Config{})
	e.owner = func(m string) string { return cmp.Or(owners[m], ann.User) }
	e.served = true
	e.start()
	return e
}

var day = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// sess is a session of n turns last active at minute min of day, in dir.
func sess(id string, min int, dir string, turns int) remote.Session {
	at := day.Add(time.Duration(min) * time.Minute)
	return remote.Session{Provider: tend.ProviderClaude, SessionID: id, Title: "session " + id, Cwd: dir, UpdatedAt: at, LastAt: at, Turns: turns}
}

func query(t *testing.T, cli *wire.Conn, sq SessionsQuery) SessionsPage {
	t.Helper()
	var page SessionsPage
	if err := callAs(cli, MSessionsQuery, "", sq, &page); err != nil {
		t.Fatalf("sessions.query %+v: %v", sq, err)
	}
	return page
}

func keys(rows []SessionRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Machine+"/"+r.SessionID)
	}
	return out
}

func answerOf(page SessionsPage, name string) *MachineAnswer {
	at := slices.IndexFunc(page.Machines, func(a MachineAnswer) bool { return a.Name == name })
	if at < 0 {
		return nil
	}
	return &page.Machines[at]
}

// sessions.query asks every machine the caller reads at once and merges their pages into one order: a page is the
// first rows of all of them, its cursor carries on where it ends on each, and paging through gives every row once.
func TestSessionsQueryMergesItsMachinesIntoOneOrder(t *testing.T) {
	e := served(t, nil)
	fakes := map[string]*fakeNode{}
	var want []string
	for i, name := range []string{"a", "b", "c"} {
		f := &fakeNode{}
		for n := range 3 {
			min := 10 * (3*n + i)
			f.sessions = append(f.sessions, sess(name+strconv.Itoa(n), min, "/w/"+name, 5))
		}
		fakes[name] = f
		e.attach(name, f)
	}
	for min := 80; min >= 0; min -= 10 {
		name := string(rune('a' + (min/10)%3))
		want = append(want, name+"/"+name+strconv.Itoa(min/30))
	}
	fakes["b"].sessions = append(fakes["b"].sessions, sess("tie", 50, "/w/b", 5)) // as new as c1: the key decides
	want = slices.Insert(want, 4, "b/tie")
	cli := e.as(ann)
	var got []string
	sq := SessionsQuery{All: true, Limit: 4}
	for pages := 0; ; pages++ {
		page := query(t, cli, sq)
		if pages == 0 {
			if len(page.Machines) != 3 || page.Facets.Machines["b"] != 4 || answerOf(page, "b").Matched != 4 || answerOf(page, "a").Total != 3 {
				t.Fatalf("each machine's counts: %+v %+v", page.Machines, page.Facets.Machines)
			}
		}
		if len(page.Rows) > 4 || pages > 5 {
			t.Fatalf("page %d: %v", pages, keys(page.Rows))
		}
		got = append(got, keys(page.Rows)...)
		if page.Next == nil {
			break
		}
		sq.After = page.Next
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged through:\n%v\nwant\n%v", got, want)
	}
	for _, f := range fakes {
		for _, p := range f.queries() {
			if p.Limit != 4 || p.Q != "" {
				t.Fatalf("a node is asked for a page as long as the caller's: %+v", p)
			}
		}
	}

	page := query(t, cli, SessionsQuery{Q: "host:b status:all", All: true, Limit: 100})
	if len(page.Machines) != 1 || page.Machines[0].Name != "b" || len(page.Rows) != 4 {
		t.Fatalf("host: asks one machine: %+v %v", page.Machines, keys(page.Rows))
	}
	if q := fakes["b"].queries(); q[len(q)-1].Q != "status:all" {
		t.Fatalf("its node reads the query without host: %q", q[len(q)-1].Q)
	}
	if !slices.ContainsFunc(page.Tokens, func(tk tend.Token) bool { return tk.Kind == tend.TokHost && tk.Value == "b" }) {
		t.Fatalf("the tokens hold host: %+v", page.Tokens)
	}
	if page := query(t, cli, SessionsQuery{Q: "host:nowhere"}); len(page.Machines) != 0 || len(page.Rows) != 0 {
		t.Fatalf("no such machine: %+v", page)
	}
	if page := query(t, e.as(bob), SessionsQuery{Q: "host:a"}); len(page.Machines) != 0 {
		t.Fatalf("a machine bob does not read is not asked, nor told of: %+v", page.Machines)
	}
	if page := query(t, cli, SessionsQuery{Q: "status:trash"}); page.Status != "" || len(page.Rows) != 0 || len(page.Machines) != len(fakes) {
		t.Fatalf("each machine answers its trash, empty here: %+v", page)
	}
	if page := query(t, cli, SessionsQuery{Q: "status:agent"}); page.Status != tend.StatusAgent || len(page.Rows) != 0 {
		t.Fatalf("one-shot runs are the TUI's: %+v", page)
	}
	if err := callAs(cli, MSessionsQuery, "", SessionsQuery{Sort: "size"}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Fatalf("an unknown order: %v", err)
	}
}

// Each machine says how it answered: in time, too late, not at all, or as a tend that has no query; the others answer
// meanwhile, and only rows that came are listed.
func TestEachMachineSaysHowItAnswered(t *testing.T) {
	was := machineWait
	machineWait = 300 * time.Millisecond
	t.Cleanup(func() { machineWait = was })
	e := served(t, map[string]string{"bobs": bob.User})
	seen := day.Add(-time.Hour)
	e.c.Expect("gone", seen)
	e.attach("ok", &fakeNode{sessions: []remote.Session{sess("k", 1, "/w", 5)}})
	e.attach("old", &fakeNode{old: true, sessions: []remote.Session{sess("o", 2, "/w", 5)}})
	e.attach("slow", &fakeNode{wait: 5 * time.Second, sessions: []remote.Session{sess("s", 3, "/w", 5)}})
	e.attach("shut", &fakeNode{refuse: wire.CodeUnauthorized})
	e.attach("broken", &fakeNode{refuse: wire.CodeInternal})
	e.attach("bobs", &fakeNode{sessions: []remote.Session{sess("b", 4, "/w", 5)}})
	start := time.Now()
	page := query(t, e.as(ann), SessionsQuery{All: true})
	if time.Since(start) > 3*time.Second {
		t.Fatalf("a slow machine holds up the page: %v", time.Since(start))
	}
	want := map[string]string{"ok": AnswerOK, "old": AnswerOld, "slow": AnswerTimeout, "shut": AnswerUnauthorized, "broken": AnswerError, "gone": AnswerOffline}
	if len(page.Machines) != len(want) {
		t.Fatalf("the machines ann reads: %+v", page.Machines)
	}
	for name, state := range want {
		a := answerOf(page, name)
		if a == nil || a.State != state || a.Owner != ann.User {
			t.Errorf("%s: %+v, want %s", name, a, state)
		}
	}
	if a := answerOf(page, "gone"); a.Since == nil || !a.Since.Equal(seen) {
		t.Errorf("offline since it was last connected: %+v", a)
	}
	if a := answerOf(page, "broken"); a.Error != wire.CodeInternal {
		t.Errorf("an error says its code: %+v", a)
	}
	if a := answerOf(page, "ok"); !a.Writable || a.Matched != 1 {
		t.Errorf("ann writes her machine with put: %+v", a)
	}
	if a := answerOf(page, "old"); a.Writable || a.Matched != 1 {
		t.Errorf("an old tend is listed whole and read only: %+v", a)
	}
	if got := keys(page.Rows); !slices.Equal(got, []string{"old/o", "ok/k"}) {
		t.Fatalf("rows: %v", got)
	}
	for _, r := range page.Rows {
		if r.Writable != (r.Machine == "ok") {
			t.Errorf("%s writable: %v", r.Machine, r.Writable)
		}
	}
}

// An old tend's whole list, read here with index.Select, lists what a tend with query lists: rows and order, project,
// task text, counts and facets, page by page.
func TestAnOldNodeListsWhatANewOneLists(t *testing.T) {
	e := served(t, nil)
	var ss []remote.Session
	for i, d := range []string{"/w/app", "/w/app/sub", "/w/other", "/w/app"} {
		s := sess("s"+strconv.Itoa(i), 10*i, d, 2+i)
		s.Tags = [][]string{{"red"}, {"red", "blue"}, nil, {"blue"}}[i]
		if i == 2 {
			s.Provider = tend.ProviderCodex
		}
		if i == 3 {
			s.FavoritedAt = new(day)
			s.ArchivedAt = new(day)
		}
		ss = append(ss, s)
	}
	newer, older := &fakeNode{sessions: ss, live: map[string]capture.Live{"s1": {Agent: "claude"}}}, &fakeNode{old: true, sessions: ss,
		live: map[string]capture.Live{"s1": {Agent: "claude"}}}
	e.attach("new", newer)
	e.attach("old", older)
	as := e.as(ann)
	if err := callAs(as, MProjectCreate, "p", ProjectCreate{ID: "p1", Name: "App"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"new", "old"} {
		if err := callAs(as, MProjectAttach, "a-"+m, ProjectAttach{Project: "p1", Machine: m, Dir: "/w/app"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"new", "old"} {
		e.ran(m, "s2", "Quokka hunt", ann.User)
	}
	type listed struct {
		Rows                    []remote.Row
		Total, Matched, Running int
		Facets                  remote.Facets
	}
	read := func(machine string, sq SessionsQuery) listed {
		var out listed
		sq.Q = "host:" + machine + " " + sq.Q
		for {
			page := query(t, as, sq)
			a := answerOf(page, machine)
			out.Total, out.Matched, out.Running, out.Facets = a.Total, a.Matched, a.Running, page.Facets.Facets
			for _, r := range page.Rows {
				out.Rows = append(out.Rows, r.Row)
			}
			if page.Next == nil {
				return out
			}
			sq.After = page.Next
		}
	}
	for _, sq := range []SessionsQuery{{All: true}, {All: true, Q: "turns:1"}, {All: true, Q: "#red turns:0"}, {All: true, Q: "project:p1 turns:0"},
		{All: true, Q: "project:none turns:0"}, {All: true, Q: "app turns:0"}, {All: true, Q: "quokka turns:0"}, {All: true, Q: "status:all turns:0"},
		{All: true, Q: "provider:codex turns:0"}, {All: true, Q: "status:live turns:0"}, {Q: "status:archived"},
		{All: true, Q: "turns:0", Sort: "turns", Limit: 1}, {All: true, Q: "status:all turns:0", Sort: "started", Limit: 2}} {
		n, o := read("new", sq), read("old", sq)
		if !reflect.DeepEqual(n, o) {
			t.Errorf("%+v:\nnew %+v\nold %+v", sq, n, o)
		}
		if sq.Q == "quokka turns:0" && (len(n.Rows) != 1 || n.Rows[0].SessionID != "s2") {
			t.Errorf("a keyword matches a session's task: %+v", n.Rows)
		}
		if sq.Q == "project:p1 turns:0" && len(n.Rows) != 2 {
			t.Errorf("project: %+v", n.Rows)
		}
	}
}

// ran gives session (on machine) a run of a new task called title, ended; the newest run on a session names its task.
func (e *env) ran(machine, session, title, owner string) string {
	e.t.Helper()
	return e.runOn(machine, session, title, owner, task.Exited)
}

// runOn gives session a run of a new task called title, in state as its node last told it.
func (e *env) runOn(machine, session, title, owner, state string) string {
	e.t.Helper()
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	n := len(e.c.st.Tasks) + 1
	id, run := "t-"+strconv.Itoa(n), "r-"+strconv.Itoa(n)
	ev := journal.NewEvent
	if err := e.c.commit(journal.System, nil,
		ev(task.ETaskCreated, task.Task{ID: id, Title: title, Machine: machine, Owner: owner, Status: task.StatusTodo, Dir: "/w"}),
		ev(task.ERunQueued, task.Run{ID: run, Task: id, Machine: machine, Agent: "claude", Profile: tend.AgentProfile{Name: "claude",
			Provider: tend.ProviderClaude}, Dir: "/w", Dispatcher: owner}),
		ev(task.ERunObserved, task.Observation{ID: run, State: state, NodeRev: 1, Session: session, Provider: tend.ProviderClaude})); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// A row names the task of the newest run that used its session, when the caller reads that task, and tells the
// machine's owner whether run.continue makes it a task now and with which agents: not while it runs, not without an
// agent that continues its provider there, not where the machine's tend cannot tell or cannot do it.
func TestARowSaysItsTaskAndWhetherItCanBecomeOne(t *testing.T) {
	e := team(t, tend.Config{Agents: []tend.AgentProfile{{Name: "deep", Provider: tend.ProviderClaude, Machine: "m"},
		{Name: "far", Provider: tend.ProviderClaude, Machine: "elsewhere"}}})
	e.owner = func(string) string { return ann.User }
	e.served = true
	e.start()
	fresh := sess("fresh", 0, "/w", 5)
	fresh.Provider = tend.ProviderCodex
	odd := sess("odd", 0, "/w", 5)
	odd.Provider = "other"
	m := &fakeNode{resume: true, sessions: []remote.Session{sess("free", 1, "/w", 5), sess("open", 2, "/w", 5), sess("working", 3, "/w", 5),
		sess("done", 4, "/w", 5), fresh, odd}, live: map[string]capture.Live{"open": {Agent: tend.ProviderClaude}}}
	e.attach("m", m)
	e.attach("old", &fakeNode{old: true, resume: true, sessions: []remote.Session{sess("o", 1, "/w", 5)}})
	e.attach("unresumed", &fakeNode{sessions: []remote.Session{sess("u", 1, "/w", 5)}})
	e.runOn("m", "working", "Working on it", ann.User, task.Running)
	e.ran("m", "done", "Write the quokka census", ann.User)
	e.ran("m", "done", "Count again", ann.User)
	m.mu.Lock()
	for i := range m.sessions {
		if m.sessions[i].SessionID == "fresh" {
			m.sessions[i].LastAt = time.Now()
		}
	}
	m.mu.Unlock()
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "m", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}

	rows := map[string]SessionRow{}
	for _, r := range query(t, e.as(ann), SessionsQuery{All: true}).Rows {
		rows[r.SessionID] = r
	}
	for id, why := range map[string]string{"free": "", "open": MakeBusy, "working": MakeBusy, "done": "", "fresh": MakeBusy, "odd": MakeNoAgent,
		"o": MakeOld, "u": MakeOld} {
		mk, provider := rows[id].Make, rows[id].Provider
		if mk == nil || mk.Why != why {
			t.Errorf("%s: make %+v, want why %q", id, mk, why)
			continue
		}
		if provider == "other" {
			if len(mk.Agents) != 0 {
				t.Errorf("%s: no agent continues it: %v", id, mk.Agents)
			}
			continue
		}
		if len(mk.Agents) == 0 || mk.Agents[0] != provider || slices.Contains(mk.Agents, "far") || slices.Contains(mk.Agents, "deep") != (rows[id].Machine == "m" && provider == tend.ProviderClaude) {
			t.Errorf("%s on %s: agents %v, its provider's first, those bound to its machine or none", id, rows[id].Machine, mk.Agents)
		}
	}
	if tl := rows["done"].Task; tl == nil || tl.Title != "Count again" {
		t.Errorf("the newest run's task: %+v", tl)
	}
	if tl := rows["working"].Task; tl == nil || tl.Title != "Working on it" || rows["free"].Task != nil {
		t.Errorf("tasks: %+v %+v", tl, rows["free"].Task)
	}
	if page := query(t, e.as(ann), SessionsQuery{Q: "again", All: true}); len(page.Rows) != 1 || page.Rows[0].SessionID != "done" {
		t.Errorf("a keyword matches the text of the task its session last worked for: %v", keys(page.Rows))
	}
	if page := query(t, e.as(ann), SessionsQuery{Q: "quokka", All: true}); len(page.Rows) != 0 {
		t.Errorf("not of an older one: %v", keys(page.Rows))
	}

	page := query(t, e.as(bob), SessionsQuery{Q: "host:m", All: true})
	if len(page.Rows) != 6 {
		t.Fatalf("bob reads m: %v", keys(page.Rows))
	}
	for _, r := range page.Rows {
		if r.Make != nil || r.Task != nil {
			t.Errorf("bob neither owns m nor reads ann's tasks: %s %+v %+v", r.SessionID, r.Make, r.Task)
		}
	}
	if page := query(t, e.as(bob), SessionsQuery{Q: "again", All: true}); len(page.Rows) != 0 {
		t.Errorf("nor does a task bob does not read match his keywords: %v", keys(page.Rows))
	}
}

// A session migrated to another machine is listed there and here, one row each: each row carries the relation its
// own machine sent, its own task and whether its own machine runs it (machine plus session, never the id alone).
func TestTheSameSessionOnTwoMachinesIsTwoRows(t *testing.T) {
	e := team(t, tend.Config{})
	e.owner = func(string) string { return ann.User }
	e.served = true
	e.start()
	away, came := sess("dup", 1, "/w", 5), sess("dup", 2, "/w", 5)
	away.Copies = []remote.Copy{{Migration: "m-1", Role: migrate.RoleTo, State: migrate.StateDone, Peer: remote.PeerRef{Name: "m2", Endpoint: "e2"}}}
	came.Copies = []remote.Copy{{Migration: "m-1", Role: migrate.RoleFrom, State: migrate.StateDone, Peer: remote.PeerRef{Name: "m1", Endpoint: "e1"}}}
	e.attach("m1", &fakeNode{resume: true, sessions: []remote.Session{away}})
	e.attach("m2", &fakeNode{resume: true, sessions: []remote.Session{came}, live: map[string]capture.Live{"dup": {Agent: tend.ProviderClaude}}})
	e.runOn("m1", "dup", "Before the move", ann.User, task.Exited)

	rows := map[string]SessionRow{}
	for _, r := range query(t, e.as(ann), SessionsQuery{All: true}).Rows {
		rows[r.Machine] = r
	}
	m1, m2 := rows["m1"], rows["m2"]
	if len(rows) != 2 || m1.SessionID != "dup" || m2.SessionID != "dup" {
		t.Fatalf("one row per machine: %v", keys(query(t, e.as(ann), SessionsQuery{All: true}).Rows))
	}
	if len(m1.Copies) != 1 || m1.Copies[0].Role != migrate.RoleTo || len(m2.Copies) != 1 || m2.Copies[0].Peer.Name != "m1" {
		t.Errorf("each row keeps its machine's relation: %+v | %+v", m1.Copies, m2.Copies)
	}
	if m1.Task == nil || m1.Task.Title != "Before the move" || m2.Task != nil {
		t.Errorf("the task stays with the machine its run used: %+v %+v", m1.Task, m2.Task)
	}
	if m1.Live != nil || m2.Live == nil || m1.Make == nil || m1.Make.Why != "" || m2.Make == nil || m2.Make.Why != MakeBusy {
		t.Errorf("running is per machine: %+v %+v | %+v %+v", m1.Live, m1.Make, m2.Live, m2.Make)
	}
}

// The same table of edits made through the TUI's path (Store.Update with tend.Patch on this machine) and through the
// server's (node.call put) leaves the same record, and sessions.query lists what the record says after each.
func TestTheSameEditsThroughTheTUIAndTheServer(t *testing.T) {
	type state struct {
		Fav, Archived, Trashed bool
		Status, Title, Sum     string
		Tags                   []string
	}
	ofRec := func(r *tend.Rec) state {
		return state{Fav: r.Favorite(), Archived: r.Archived(), Status: r.Status, Title: r.Title, Sum: r.Summary, Tags: r.Tags}
	}
	ofRow := func(r remote.Row) state {
		return state{Fav: r.FavoritedAt != nil, Archived: r.ArchivedAt != nil, Trashed: r.DeletedAt != nil, Status: r.Status, Title: r.Title,
			Sum: r.Summary, Tags: r.Tags}
	}
	run := func(t *testing.T, teamMode, server bool) []state {
		d, err := fixture.Build(filepath.Join(t.TempDir(), "machine"), day)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("TEND_HOME", d.Home)
		t.Setenv("CLAUDE_CONFIG_DIR", d.Claude)
		t.Setenv("CODEX_HOME", d.Codex)
		var e *env
		who := Owner
		if teamMode {
			e, who = team(t, tend.Config{}), ann
		} else {
			e = newEnv(t, tend.Config{})
		}
		e.start()
		at := slices.IndexFunc(d.Sessions, func(s fixture.Session) bool {
			return s.Listed && !s.Favorite && !s.Agent && s.Provider == tend.ProviderClaude
		})
		target := d.Sessions[at]
		cli := e.as(who)
		row := func() remote.Row {
			t.Helper()
			page := query(t, cli, SessionsQuery{Q: "status:all turns:0", All: true, Limit: pageMax})
			at := slices.IndexFunc(page.Rows, func(r SessionRow) bool { return r.SessionID == target.ID })
			if at < 0 {
				t.Fatalf("%s is not listed: %+v", target.Name, page.Machines)
			}
			return page.Rows[at].Row
		}
		record := func() *tend.Rec {
			s, err := tend.Open()
			if err != nil {
				t.Fatal(err)
			}
			return s.BySession(target.Provider, target.ID)
		}
		apply := func(p tend.Patch, expect *time.Time) error {
			if server {
				return callAs(cli, MNodeCall, "", NodeCall{Machine: Local, Method: remote.MPut,
					Params: mustJSON(remote.PutParams{Ref: remote.Ref{Provider: target.Provider, SessionID: target.ID}, Patch: p, Expect: expect})}, nil)
			}
			s, err := tend.Open()
			if err != nil {
				return err
			}
			r := s.BySession(target.Provider, target.ID)
			if r == nil {
				r = row().Rec("")
			}
			_, err = s.Update(r, func(r *tend.Rec) { p.Apply(r, time.Now()) })
			return err
		}
		edit := tend.Patch{Title: new("Renamed in two places"), Tags: new([]string{"Alpha", "beta", "alpha"}), Summary: new("  what it did  ")}
		var befores []*tend.Rec
		before := func() *tend.Rec {
			if r := record(); r != nil {
				cp := *r
				return &cp
			}
			return row().Rec("")
		}
		steps := []func() (tend.Patch, *time.Time){
			func() (tend.Patch, *time.Time) { return tend.Patch{Favorite: new(true)}, nil },
			func() (tend.Patch, *time.Time) { return tend.Patch{Status: new(tend.StatusDone)}, nil },
			func() (tend.Patch, *time.Time) { return tend.Patch{Archived: new(true)}, nil },
			func() (tend.Patch, *time.Time) { return edit, new(row().UpdatedAt) },
			func() (tend.Patch, *time.Time) { return edit.Undo(befores[3]), nil },
			func() (tend.Patch, *time.Time) { return tend.Patch{Archived: new(true)}.Undo(befores[2]), nil },
			func() (tend.Patch, *time.Time) { return tend.Patch{Favorite: new(true)}.Undo(befores[0]), nil },
		}
		var out []state
		for i, step := range steps {
			befores = append(befores, before())
			p, expect := step()
			if err := apply(p, expect); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			rec := record()
			if rec == nil {
				t.Fatalf("step %d: no record", i)
			}
			if got, listed := ofRec(rec), ofRow(row()); !reflect.DeepEqual(got, listed) {
				t.Fatalf("step %d: the record %+v, listed %+v", i, got, listed)
			}
			out = append(out, ofRec(rec))
		}
		if server {
			err := apply(tend.Patch{Title: new("late")}, new(day))
			if wire.Code(err) != wire.CodeStale || record().Title == "late" {
				t.Fatalf("an edit of a record changed since it was read: %v", err)
			}
		}

		ref := remote.Ref{Provider: target.Provider, SessionID: target.ID}
		listed := func(q string) []remote.Row {
			t.Helper()
			page := query(t, cli, SessionsQuery{Q: q + " turns:0", All: true, Limit: pageMax, Fresh: true})
			var rows []remote.Row
			for _, r := range page.Rows {
				if r.SessionID == target.ID {
					rows = append(rows, r.Row)
				}
			}
			return rows
		}
		move := func(method string) error {
			if server {
				return callAs(cli, MNodeCall, "", NodeCall{Machine: Local, Method: method, Params: mustJSON(ref)}, nil)
			}
			s, err := tend.Open()
			if err != nil {
				return err
			}
			idx, err := index.Open()
			if err != nil {
				return err
			}
			if method == remote.MTrash {
				_, _, err = index.TrashSession(s, idx, s.BySession(target.Provider, target.ID))
			} else {
				_, _, err = index.RestoreSession(s, idx, target.Provider, target.ID)
			}
			return err
		}
		if err := move(remote.MTrash); err != nil {
			t.Fatalf("trash: %v", err)
		}
		gone := listed("status:trash")
		if r := record(); r != nil || len(listed("status:all")) != 0 || len(gone) != 1 || gone[0].DeletedAt == nil {
			t.Fatalf("trashed: the record %+v, in the trash %+v", r, gone)
		}
		var page capture.Page
		if err := callAs(cli, MNodeCall, "", NodeCall{Machine: Local, Method: remote.MMessages,
			Params: mustJSON(remote.MessagesParams{Ref: ref, Before: -1, N: 2})}, &page); err != nil || len(page.Msgs) == 0 {
			t.Fatalf("a trashed session's conversation reads: %+v %v", page, err)
		}
		out = append(out, ofRow(gone[0]))
		if err := move(remote.MRestore); err != nil {
			t.Fatalf("restore: %v", err)
		}
		rec := record()
		if rec == nil || len(listed("status:trash")) != 0 {
			t.Fatalf("restored: the record %+v", rec)
		}
		if got, back := ofRec(rec), listed("status:all"); len(back) != 1 || !reflect.DeepEqual(got, ofRow(back[0])) {
			t.Fatalf("restored: the record %+v, listed %+v", got, back)
		}
		return append(out, ofRec(rec))
	}
	for _, teamMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("team=%v", teamMode), func(t *testing.T) {
			var tui, server []state
			t.Run("tui", func(t *testing.T) { tui = run(t, teamMode, false) })
			t.Run("server", func(t *testing.T) { server = run(t, teamMode, true) })
			if len(tui) == 0 || !reflect.DeepEqual(tui, server) {
				t.Fatalf("through the TUI:\n%+v\nthrough the server:\n%+v", tui, server)
			}
			if last := tui[len(tui)-3]; last.Fav || last.Archived || last.Title == "Renamed in two places" || last.Status != tend.StatusDone {
				t.Fatalf("undone: %+v", last)
			}
			if gone, back := tui[len(tui)-2], tui[len(tui)-1]; !gone.Trashed || back.Trashed || back.Title != tui[len(tui)-3].Title {
				t.Fatalf("trashed %+v, restored %+v", gone, back)
			}
			if mid := tui[3]; !slices.Equal(mid.Tags, []string{"alpha", "beta"}) || mid.Sum != "what it did" {
				t.Fatalf("the edit as Patch.Apply normalizes it: %+v", mid)
			}
		})
	}
}

// put reaches a machine's node for its owner alone: whom its sessions are shared with read and do not write, admins do
// not, nor does anyone a machine under local; local itself does, as does mode 1's one user.
func TestOnlyAMachinesOwnerWritesItsRecords(t *testing.T) {
	e := served(t, map[string]string{"under-local": Owner.User})
	mine, local := &fakeNode{sessions: []remote.Session{sess("m", 1, "/w", 5)}, resume: true}, &fakeNode{sessions: []remote.Session{sess("l", 1, "/w", 5)}}
	e.attach("mine", mine)
	e.attach("under-local", local)
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	put := func(p Principal, machine, id string) error {
		return callAs(e.as(p), MNodeCall, "", NodeCall{Machine: machine, Method: remote.MPut,
			Params: mustJSON(remote.PutParams{Ref: remote.Ref{Provider: tend.ProviderClaude, SessionID: id}, Patch: tend.Patch{Favorite: new(true)}})}, nil)
	}
	for _, c := range []struct {
		p       Principal
		machine string
		want    string
	}{{ann, "mine", ""}, {bob, "mine", wire.CodeUnauthorized}, {root, "mine", wire.CodeUnauthorized}, {cy, "mine", wire.CodeUnauthorized},
		{root, "under-local", wire.CodeUnauthorized}, {ann, "under-local", wire.CodeUnauthorized}, {Owner, "under-local", ""}} {
		id := map[string]string{"mine": "m", "under-local": "l"}[c.machine]
		if err := put(c.p, c.machine, id); wire.Code(err) != c.want {
			t.Errorf("%s puts on %s: %v, want %q", c.p.User, c.machine, err, c.want)
		}
	}
	if len(mine.puts) != 1 || len(local.puts) != 1 {
		t.Fatalf("only the owners' puts reach the nodes: %d %d", len(mine.puts), len(local.puts))
	}
	if err := callAs(e.as(bob), MNodeCall, "", NodeCall{Machine: "mine", Method: remote.MQuery, Params: mustJSON(remote.QueryParams{Limit: 5})}, nil); err != nil {
		t.Errorf("bob reads mine through node.call query: %v", err)
	}
	if err := callAs(e.as(bob), MNodeCall, "", NodeCall{Machine: "mine", Method: remote.MHits, Params: mustJSON(remote.HitsParams{})}, nil); wire.Code(err) == wire.CodeUnauthorized {
		t.Errorf("hits is a read: %v", err)
	}

	for p, want := range map[Principal]bool{ann: true, bob: false} {
		page := query(t, e.as(p), SessionsQuery{Q: "host:mine", All: true})
		if len(page.Rows) != 1 || page.Rows[0].Writable != want || page.Machines[0].Writable != want || (page.Rows[0].Make != nil) != want {
			t.Errorf("%s's row of mine: writable %v, make %+v; %+v", p.User, page.Rows[0].Writable, page.Rows[0].Make, page.Machines)
		}
	}
	runs := &fakeNode{share: node.ShareRuns, sessions: []remote.Session{sess("r", 1, "/w", 5)}}
	e.attach("runs-only", runs)
	if page := query(t, e.as(ann), SessionsQuery{Q: "host:runs-only", All: true}); len(page.Rows) != 1 || !page.Rows[0].Writable ||
		page.Machines[0].Share != node.ShareRuns {
		t.Errorf("a node that answers its runs' sessions lists only those, and they are writable: %+v", page)
	}

	single := newEnv(t, tend.Config{})
	single.start()
	f := &fakeNode{sessions: []remote.Session{sess("x", 1, "/w", 5)}}
	single.attach("far", f)
	if err := callAs(single.as(Owner), MNodeCall, "", NodeCall{Machine: "far", Method: remote.MPut,
		Params: mustJSON(remote.PutParams{Ref: remote.Ref{Provider: tend.ProviderClaude, SessionID: "x"}, Patch: tend.Patch{Archived: new(true)}})}, nil); err != nil || len(f.puts) != 1 {
		t.Errorf("mode 1's user writes every machine: %v", err)
	}
}

// trash and restore pass the same gate as put: a machine's owner alone, never whom its sessions are shared with, an
// admin, or anyone a machine under local; mode 1's one user trashes on every machine.
func TestOnlyAMachinesOwnerTrashesAndRestores(t *testing.T) {
	e := served(t, map[string]string{"under-local": Owner.User})
	mine := &fakeNode{sessions: []remote.Session{sess("m", 1, "/w", 5)}}
	local := &fakeNode{sessions: []remote.Session{sess("l", 1, "/w", 5)}}
	e.attach("mine", mine)
	e.attach("under-local", local)
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	call := func(p Principal, machine, method, id string) error {
		return callAs(e.as(p), MNodeCall, "", NodeCall{Machine: machine, Method: method,
			Params: mustJSON(remote.Ref{Provider: tend.ProviderClaude, SessionID: id})}, nil)
	}
	for _, method := range []string{remote.MTrash, remote.MRestore} {
		for _, c := range []struct {
			p       Principal
			machine string
		}{{bob, "mine"}, {root, "mine"}, {cy, "mine"}, {root, "under-local"}, {ann, "under-local"}} {
			if err := call(c.p, c.machine, method, "m"); wire.Code(err) != wire.CodeUnauthorized {
				t.Errorf("%s %s on %s: %v", c.p.User, method, c.machine, err)
			}
		}
	}
	if err := call(ann, "mine", remote.MTrash, "m"); err != nil {
		t.Fatalf("ann trashes on her machine: %v", err)
	}
	if err := call(ann, "mine", remote.MRestore, "m"); err != nil {
		t.Fatalf("ann restores on her machine: %v", err)
	}
	if err := call(Owner, "under-local", remote.MTrash, "l"); err != nil {
		t.Fatalf("local trashes on a machine under local: %v", err)
	}
	if !slices.Equal(mine.trashes, []string{"trash:m", "restore:m"}) || !slices.Equal(local.trashes, []string{"trash:l"}) {
		t.Fatalf("only the owners' calls reach the nodes: %v %v", mine.trashes, local.trashes)
	}

	single := newEnv(t, tend.Config{})
	single.start()
	f := &fakeNode{sessions: []remote.Session{sess("x", 1, "/w", 5)}}
	single.attach("far", f)
	if err := callAs(single.as(Owner), MNodeCall, "", NodeCall{Machine: "far", Method: remote.MTrash,
		Params: mustJSON(remote.TrashParams{Ref: remote.Ref{Provider: tend.ProviderClaude, SessionID: "x"}})}, nil); err != nil || len(f.trashes) != 1 {
		t.Errorf("mode 1's user trashes on every machine: %v", err)
	}
}

// Each machine answers status:trash with its own trash: its rows say when they were deleted and are neither written
// nor made into tasks; a machine whose tend has no trash says old instead of an empty trash. A machine answer says
// whether the caller may trash there and after how many days its trash is purged.
func TestTheTrashIsListedPerMachine(t *testing.T) {
	e := served(t, map[string]string{"bobs": bob.User})
	deleted := day.Add(-2 * time.Hour)
	gone := remote.Row{Session: sess("t1", 3, "/w", 5), DeletedAt: &deleted}
	mine := &fakeNode{sessions: []remote.Session{sess("k", 1, "/w", 5)}, trashed: []remote.Row{gone}, trashDays: 7, resume: true}
	noTrash := &fakeNode{noTrash: true, sessions: []remote.Session{sess("n", 2, "/w", 5)}}
	e.attach("mine", mine)
	e.attach("no-trash", noTrash)
	e.attach("old", &fakeNode{old: true, sessions: []remote.Session{sess("o", 2, "/w", 5)}})
	e.attach("bobs", &fakeNode{sessions: []remote.Session{sess("b", 4, "/w", 5)}, trashed: []remote.Row{{Session: sess("bt", 1, "/w", 5), DeletedAt: &deleted}}})
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	asked := len(noTrash.queries())

	page := query(t, e.as(ann), SessionsQuery{Q: "status:trash"})
	if got := keys(page.Rows); !slices.Equal(got, []string{"mine/t1"}) || page.Status != "" {
		t.Fatalf("ann's trash: %v %+v", got, page)
	}
	if r := page.Rows[0]; r.DeletedAt == nil || !r.DeletedAt.Equal(deleted) || r.Writable || r.Make != nil {
		t.Errorf("a trashed row: deleted_at %v, writable %v, make %+v", r.DeletedAt, r.Writable, r.Make)
	}
	if a := answerOf(page, "mine"); a.State != AnswerOK || !a.Trash || a.TrashDays != 7 || a.Matched != 1 {
		t.Errorf("mine: %+v", a)
	}
	for _, name := range []string{"no-trash", "old"} {
		if a := answerOf(page, name); a == nil || a.State != AnswerOld || a.Trash || a.Matched != 0 {
			t.Errorf("%s: a tend without trash is old in the trash: %+v", name, a)
		}
	}
	if len(noTrash.queries()) != asked {
		t.Errorf("a node without trash is not asked for its trash: %+v", noTrash.queries())
	}

	page = query(t, e.as(ann), SessionsQuery{All: true})
	if a := answerOf(page, "mine"); a.State != AnswerOK || !a.Trash || a.TrashDays != 7 {
		t.Errorf("every answer says whether the caller trashes there: %+v", a)
	}
	if a := answerOf(page, "no-trash"); a.State != AnswerOK || a.Trash || !a.Writable {
		t.Errorf("a node with put and no trash writes and does not trash: %+v", a)
	}

	shared := query(t, e.as(bob), SessionsQuery{Q: "status:trash host:mine"})
	if a := answerOf(shared, "mine"); a == nil || a.Trash || !slices.Equal(keys(shared.Rows), []string{"mine/t1"}) {
		t.Errorf("bob reads mine's trash and may not restore there: %+v %v", a, keys(shared.Rows))
	}
	if b := query(t, e.as(bob), SessionsQuery{Q: "status:trash host:bobs"}); !answerOf(b, "bobs").Trash {
		t.Errorf("bob trashes on his own machine: %+v", b.Machines)
	}
}

// A change of a machine's session records moves its records_rev, which machines.watch pushes to whoever reads its
// sessions and to nobody else.
func TestRecordsRevReachesOnlyThoseWhoReadTheSessions(t *testing.T) {
	e := served(t, nil)
	end := e.attach("mine", &fakeNode{})
	if err := callAs(e.as(ann), MMachineShare, "share", task.Share{Machine: "mine", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "mine", Users: []string{dee.User}}, nil); err != nil {
		t.Fatal(err)
	}
	listed := func(ms MachineList) *Machine {
		at := slices.IndexFunc(ms.Items, func(m Machine) bool { return m.Name == "mine" })
		if at < 0 {
			return nil
		}
		return &ms.Items[at]
	}
	watches := map[Principal]*topicWatch{}
	for _, p := range []Principal{ann, bob, dee} {
		w := watchTopic(t, e.as(p), MMachinesWatch)
		var ms MachineList
		w.until(&ms, func() bool {
			m := listed(ms)
			return m != nil && m.State == MachineConnected && m.Sessions == (p != bob)
		})
		watches[p] = w
	}
	end.Push(node.MChanged, map[string]any{"records": true})
	end.Push(node.MChanged, map[string]any{"records": true})
	for _, p := range []Principal{ann, dee} {
		var ms MachineList
		watches[p].until(&ms, func() bool { return listed(ms).RecordsRev == 2 })
	}
	deadline := time.After(machinesWait * 5)
	for {
		select {
		case p := <-watches[bob].pushes:
			var ms MachineList
			if p.Decode(&ms) != nil || listed(ms) == nil || listed(ms).RecordsRev != 0 {
				t.Fatalf("bob sees mine but does not read its sessions: %s", p.Params)
			}
			continue
		case <-deadline:
		}
		break
	}
	var ms Machines
	if err := callAs(e.as(dee), MMachineList, "", MachinesParams{}, &ms); err != nil || ms.Machines[0].RecordsRev != 2 {
		t.Fatalf("machine.list says it too: %+v %v", ms.Machines, err)
	}
}

// sessions.grep asks each machine and merges by tier, then by rank within each machine: every hit matching all
// keywords in one message first, each machine's best before anyone's second.
func TestSessionsGrepInterleavesTheMachinesRanks(t *testing.T) {
	e := served(t, nil)
	hit := func(id string, whole bool) remote.GrepHit {
		return remote.GrepHit{Row: remote.Row{Session: sess(id, 0, "/w", 5)}, AllInOne: whole, Hits: 1}
	}
	e.attach("a", &fakeNode{grep: &remote.GrepResult{Hits: []remote.GrepHit{hit("a1", true), hit("a2", false), hit("a3", true)},
		Building: &remote.Progress{Done: 3, Total: 9}, Fixes: []string{"colour"}}})
	e.attach("b", &fakeNode{grep: &remote.GrepResult{Hits: []remote.GrepHit{hit("b1", false), hit("b2", true), hit("b3", false)},
		Fixes: []string{"colour", "colr"}, TooLong: true}})
	e.attach("c", &fakeNode{})
	var found SessionsFound
	if err := callAs(e.as(ann), MSessionsGrep, "", SessionsGrep{Q: "color", Limit: 5}, &found); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range found.Hits {
		got = append(got, h.Machine+"/"+h.Row.SessionID)
	}
	if want := []string{"a/a1", "b/b2", "a/a3", "a/a2", "b/b1"}; !slices.Equal(got, want) {
		t.Fatalf("merged: %v, want %v", got, want)
	}
	if !slices.Equal(found.Fixes, []string{"colour", "colr"}) || !found.TooLong {
		t.Fatalf("fixes and too_long: %+v", found)
	}
	if a := answerOf(SessionsPage{Machines: found.Machines}, "a"); a.State != AnswerOK || a.Building == nil || a.Building.Total != 9 {
		t.Fatalf("a's store is being built: %+v", a)
	}
	if a := answerOf(SessionsPage{Machines: found.Machines}, "c"); a.State != AnswerOld {
		t.Fatalf("a node without grep is not searched: %+v", a)
	}
}

// A hit of sessions.grep says whether its session can become a task as a listed row does, to the machine's owner only.
func TestAHitSaysWhetherItsSessionCanBecomeATask(t *testing.T) {
	e := served(t, nil)
	hit := func(id string, live bool) remote.GrepHit {
		h := remote.GrepHit{Row: remote.Row{Session: sess(id, 0, "/w", 5)}, Hits: 1}
		if live {
			h.Row.Live = &capture.Live{Agent: tend.ProviderClaude}
		}
		return h
	}
	m := &fakeNode{resume: true, grep: &remote.GrepResult{Hits: []remote.GrepHit{hit("free", false), hit("open", true), hit("working", false)}}}
	e.attach("m", m)
	e.attach("unresumed", &fakeNode{grep: &remote.GrepResult{Hits: []remote.GrepHit{hit("u", false)}}})
	e.runOn("m", "working", "Working on it", ann.User, task.Running)
	if err := callAs(e.as(ann), MMachineSessions, "scope", task.SessionsSet{Machine: "m", Users: []string{bob.User}}, nil); err != nil {
		t.Fatal(err)
	}
	grep := func(cli *wire.Conn) map[string]SessionHit {
		var found SessionsFound
		if err := callAs(cli, MSessionsGrep, "", SessionsGrep{Q: "color"}, &found); err != nil {
			t.Fatal(err)
		}
		out := map[string]SessionHit{}
		for _, h := range found.Hits {
			out[h.Row.SessionID] = h
		}
		return out
	}
	hits := grep(e.as(ann))
	for id, why := range map[string]string{"free": "", "open": MakeBusy, "working": MakeBusy, "u": MakeOld} {
		if mk := hits[id].Make; mk == nil || mk.Why != why || mk.Agents[0] != tend.ProviderClaude {
			t.Errorf("%s: make %+v, want why %q", id, mk, why)
		}
	}
	for id, h := range grep(e.as(bob)) {
		if h.Make != nil {
			t.Errorf("bob does not own m: %s %+v", id, h.Make)
		}
	}
}

// people.names answers only the people the caller has reason to know: themselves, the owners of machines they see,
// the people of projects they see, the owners and approvers of tasks they read; a disabled one is told so.
func TestPeopleNamesAreThoseTheCallerKnows(t *testing.T) {
	e := stage(t, true)
	e.usersM.Lock()
	e.users["u_pat"] = User{ID: "u_pat", Email: "pat@example.com"}
	e.users[dee.User] = User{ID: dee.User, Name: "dee", Disabled: true}
	e.usersM.Unlock()
	ids := []string{root.User, ann.User, bob.User, cy.User, dee.User, eve.User, "u_pat", "u_nobody"}
	ask := func(p Principal) People {
		t.Helper()
		var out People
		if err := callAs(e.as(p), MPeopleNames, "", PeopleParams{IDs: ids}, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	known := func(pp People) []string {
		return slices.Sorted(func(yield func(string) bool) {
			for id := range pp.Names {
				if !yield(id) {
					return
				}
			}
		})
	}
	if got := known(ask(cy)); !slices.Equal(got, []string{cy.User}) {
		t.Errorf("cy, in nothing: %v", got)
	}
	if got := ask(bob); !slices.Equal(known(got), []string{ann.User, bob.User, dee.User}) || !slices.Equal(got.Disabled, []string{dee.User}) ||
		got.Names[ann.User] != "ann" {
		t.Errorf("bob knows p1's people and who owns the machines shared with it: %+v", got)
	}
	if got := known(ask(eve)); !slices.Equal(got, []string{ann.User, eve.User}) {
		t.Errorf("eve reads ann's machines' sessions: %v", got)
	}
	if err := callAs(e.as(ann), MTaskCreate, "pat", TaskCreate{Title: "for pat", Dir: "/w"}, nil); err != nil {
		t.Fatal(err)
	}
	e.c.mu.Lock()
	for _, tk := range e.c.st.Tasks {
		if tk.Title == "for pat" {
			e.c.commit(journal.System, nil, journal.NewEvent(task.ETaskEdited, task.TaskEdit{ID: tk.ID, Approver: new("u_pat")}))
		}
	}
	e.c.mu.Unlock()
	if got := ask(ann); got.Names["u_pat"] != "pat" {
		t.Errorf("ann reads a task pat approves; pat's name is the email's first part: %+v", got)
	}
	if got := ask(bob); got.Names["u_pat"] != "" {
		t.Errorf("bob does not read ann's own task: %+v", got)
	}
	if err := callAs(e.as(cy), MPeopleNames, "", PeopleParams{IDs: make([]string, maxNames+1)}, nil); wire.Code(err) != wire.CodeBadRequest {
		t.Errorf("too many ids: %v", err)
	}

	single := newEnv(t, tend.Config{})
	single.start()
	var pp People
	if err := callAs(single.as(Owner), MPeopleNames, "", PeopleParams{IDs: []string{Owner.User, "u_x"}}, &pp); err != nil ||
		!reflect.DeepEqual(pp.Names, map[string]string{Owner.User: Owner.User}) {
		t.Errorf("mode 1 knows its one user: %+v %v", pp, err)
	}
}

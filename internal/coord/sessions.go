package coord

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// SessionsQuery is sessions.query: Q read as tend.Parse reads it over every machine whose sessions the caller reads
// (host: names one), a page of rows in Sort's order after After.
type SessionsQuery struct {
	Q     string          `json:"q"`
	All   bool            `json:"all,omitempty"`   // not only favorites
	Sort  string          `json:"sort,omitempty"`  // active (default) | started | favorited | turns
	Limit int             `json:"limit,omitempty"` // 1..500, default 100
	After *SessionsCursor `json:"after,omitempty"`
	Fresh bool            `json:"fresh,omitempty"` // each node refreshes its index first
}

// SessionsCursor is where a row of sessions.query falls: its order's cursor and its machine.
type SessionsCursor struct {
	remote.Cursor
	Machine string `json:"machine"`
}

// SessionRow is a row of sessions.query.
type SessionRow struct {
	Machine string `json:"machine"`
	// Writable: node.call put may change its record: the caller owns the machine, and its node has put and answers
	// this session.
	Writable bool      `json:"writable,omitempty"`
	Task     *TaskLink `json:"task,omitempty"` // the task of the newest run that used it, when the caller reads that task
	Make     *MakeTask `json:"make,omitempty"` // whether run.continue makes it a task: the machine's owner only
	remote.Row
}

// TaskLink names the task a session worked for.
type TaskLink struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Stage string `json:"stage,omitempty"`
}

// MakeTask is whether a session can become a task now (run.continue), and with which agents.
type MakeTask struct {
	Why    string   `json:"why,omitempty"`    // "": it can; MakeBusy | MakeNoAgent | MakeOld
	Agents []string `json:"agents,omitempty"` // the profiles that can go on with it there, the one named after its provider first
}

// Why a session cannot become a task now, in MakeTask.Why.
const (
	MakeBusy    = "busy"     // it runs: open in a terminal or an app, a run on it, or a Codex session written to just now
	MakeNoAgent = "no_agent" // no agent can go on with it there
	MakeOld     = "old"      // its machine's tend cannot tell whether it runs, or cannot start a run in a session
)

// MachineAnswer is how one machine answered sessions.query or sessions.grep.
type MachineAnswer struct {
	Name     string     `json:"name"`
	Owner    string     `json:"owner,omitempty"` // team mode: its owner's id (people.names has the name)
	State    string     `json:"state"`           // ok | offline | old | timeout | unauthorized | error
	Error    string     `json:"error,omitempty"` // error: the code
	Since    *time.Time `json:"since,omitempty"` // offline: when it was last connected, as far as is known
	Total    int        `json:"total"`
	Matched  int        `json:"matched"`
	Running  int        `json:"running"`
	Writable bool       `json:"writable,omitempty"` // put may change its rows: the caller owns it, its node has put and answers its sessions
	// Trash: trash and restore may move its rows, as Writable for put.
	Trash     bool   `json:"trash,omitempty"`
	TrashDays int    `json:"trash_days,omitempty"` // its trash purges a session after this many days (0: never)
	Share     string `json:"share_sessions,omitempty"`
	// Building: sessions.grep only, its message-search store is still being built; what it had is listed.
	Building *remote.Progress `json:"building,omitempty"`
}

// How a machine answered, in MachineAnswer.State.
const (
	AnswerOK           = "ok"
	AnswerOffline      = "offline"
	AnswerOld          = "old" // its tend predates the method: listed from its whole list, not searched, not written; in status:trash, listed not at all
	AnswerTimeout      = "timeout"
	AnswerUnauthorized = "unauthorized" // its node answers none of its sessions (share_sessions: none)
	AnswerError        = "error"
)

// SessionFacets are the machines' facets summed, and each machine's matched rows.
type SessionFacets struct {
	remote.Facets
	Machines map[string]int `json:"machines,omitempty"`
}

// SessionsPage answers sessions.query.
type SessionsPage struct {
	Rows     []SessionRow    `json:"rows"`
	Next     *SessionsCursor `json:"next,omitempty"`
	Machines []MachineAnswer `json:"machines"`
	Facets   SessionFacets   `json:"facets"`
	Tokens   []tend.Token    `json:"tokens"`           // Q as Parse reads it, host: included
	Status   string          `json:"status,omitempty"` // agent: rows not listed here (the TUI's only)
}

// SessionsGrep is sessions.grep: message search over every machine whose sessions the caller reads.
type SessionsGrep struct {
	Q     string `json:"q"` // the text after >
	All   bool   `json:"all,omitempty"`
	Limit int    `json:"limit,omitempty"` // 1..500, default 50
}

// SessionHit is a hit of sessions.grep.
type SessionHit struct {
	Machine string `json:"machine"`
	remote.GrepHit
}

// SessionsFound answers sessions.grep: hits that matched every keyword in one message first, then the rest, each tier
// taking every machine's best before anyone's second (machines rank by their own corpus, which other machines'
// scores do not compare to).
type SessionsFound struct {
	Hits     []SessionHit    `json:"hits"`
	Machines []MachineAnswer `json:"machines"`
	Fixes    []string        `json:"fixes,omitempty"`
	TooLong  bool            `json:"too_long,omitempty"`
}

const (
	sessionsLimit = 100
	grepLimit     = 50
	pageMax       = 500
)

// machineWait bounds each machine's answer: a slow one is told as timeout, the others answer meanwhile.
var machineWait = 5 * time.Second

// sessionsOf is a machine a sessions.query or sessions.grep asks, with what the caller's view gives its node.
type sessionsOf struct {
	name     string
	owner    string // team mode
	owned    bool   // the caller owns it
	since    *time.Time
	goos     string
	projects map[string]*task.Project // the caller's projects with a directory there
	dirs     []remote.ProjectDirs
	tasks    map[string]*TaskLink // session id → the task of the newest run that used it there, the caller reads
	busy     map[string]bool      // sessions an open run there uses
	also     map[string]string    // session id → its task's text, when the query has keywords
}

// sessionMachines are the machines whose sessions p reads (one when host names it, every one for "" or all), not
// retired, this machine first. The caller holds mu.
func (c *Coord) sessionMachines(p Principal, host string, words bool) []*sessionsOf {
	seen := map[string]*task.Project{}
	for id, pr := range c.st.Projects {
		if c.seesProject(p, pr) {
			seen[id] = &task.Project{ID: pr.ID, Name: pr.Name, Repos: pr.Repos} // a project edit replaces Repos, never changes it in place
		}
	}
	var out []*sessionsOf
	for _, mv := range c.machineList() {
		name := mv.Name
		if host != "" && host != tend.HostAll && !strings.EqualFold(name, host) || !c.readsSessions(p, name) || c.retired(name) {
			continue
		}
		m := c.ms[name]
		s := &sessionsOf{name: name, owned: c.ownerOf(name) == p.User, goos: m.hello.OS, projects: map[string]*task.Project{},
			tasks: map[string]*TaskLink{}, busy: map[string]bool{}}
		if c.team() {
			s.owner = c.ownerOf(name)
		}
		if !m.seenAt.IsZero() {
			s.since = new(m.seenAt)
		}
		s.dirs = remote.ProjectDirsOn(seen, name)
		for _, d := range s.dirs {
			s.projects[d.ID] = seen[d.ID]
		}
		out = append(out, s)
	}
	c.sessionTasks(p, out, words)
	return out
}

// sessionTasks fills each machine's tasks, busy sessions and keyword text: a session's task is that of the newest run
// that used it (or is queued to go on with it) whose task p reads. The caller holds mu.
func (c *Coord) sessionTasks(p Principal, ms []*sessionsOf, words bool) {
	by := map[string]*sessionsOf{}
	for _, s := range ms {
		by[s.name] = s
	}
	newest := map[*sessionsOf]task.Newest[string]{}
	reads := map[string]bool{}
	for _, r := range c.st.Runs {
		s := by[r.Machine]
		session := task.RunSession(r)
		if s == nil || session == "" {
			continue
		}
		if task.Open(r.State) {
			s.busy[session] = true
		}
		read, ok := reads[r.Task]
		if !ok {
			read = c.canRead(c.st, p, c.st.Tasks[r.Task])
			reads[r.Task] = read
		}
		if !read {
			continue
		}
		if newest[s] == nil {
			newest[s] = task.Newest[string]{}
		}
		newest[s].Add(session, r)
	}
	for s, runs := range newest {
		for session, r := range runs {
			t := c.st.Tasks[r.Task]
			s.tasks[session] = &TaskLink{ID: t.ID, Title: t.Title, Stage: t.Stage}
			if words {
				if s.also == nil {
					s.also = map[string]string{}
				}
				s.also[session] = t.ID + " " + t.Title
			}
		}
	}
}

// withoutHost is q without its host: words, which pick machines here; q itself when it has none.
func withoutHost(q string, toks []tend.Token) string {
	if !slices.ContainsFunc(toks, func(t tend.Token) bool { return t.Kind == tend.TokHost }) {
		return q
	}
	var kept []string
	for _, t := range toks {
		if t.Kind != tend.TokHost {
			kept = append(kept, t.Text)
		}
	}
	return strings.Join(kept, " ")
}

func pageSize(n, def int) int {
	if n <= 0 {
		return def
	}
	return min(n, pageMax)
}

// reachFor is machine name's hello once it is connected, within ctx; the state to answer otherwise.
func (c *Coord) reachFor(ctx context.Context, s *sessionsOf) (remote.Hello, MachineAnswer, bool) {
	a := MachineAnswer{Name: s.name, Owner: s.owner}
	c.mu.Lock()
	m := c.ms[s.name]
	c.mu.Unlock()
	if m == nil {
		a.State = AnswerOffline
		return remote.Hello{}, a, false
	}
	if _, err := c.reach(ctx, m); err != nil {
		c.failedAnswer(&a, s, err)
		return remote.Hello{}, a, false
	}
	c.mu.Lock()
	h := m.hello
	c.mu.Unlock()
	a.Share = h.Share
	a.Writable = s.owned && slices.Contains(h.Methods, remote.MPut) && h.Share != node.ShareNone
	a.Trash = s.owned && slices.Contains(h.Methods, remote.MTrash) && h.Share != node.ShareNone
	return h, a, true
}

// failedAnswer says why a machine gave no answer.
func (c *Coord) failedAnswer(a *MachineAnswer, s *sessionsOf, err error) {
	a.Writable, a.Trash = false, false
	switch code := wire.Code(err); {
	case code == wire.CodeTimeout, errors.Is(err, context.DeadlineExceeded):
		a.State = AnswerTimeout
	case code == wire.CodeUnauthorized:
		a.State = AnswerUnauthorized
	case code == wire.CodeOffline, code == wire.CodeClosed, code == wire.CodeAuth, code == wire.CodeHostKey, code == wire.CodeNoTend:
		a.State, a.Since = AnswerOffline, s.since
	default:
		a.State, a.Error = AnswerError, cmp.Or(code, wire.CodeInternal)
	}
}

// machineRows is one machine's part of a sessions.query page.
type machineRows struct {
	answer MachineAnswer
	rows   []SessionRow
	at     []tend.Cursor // each row's place in the order
	more   bool
	facets remote.Facets
}

func (c *Coord) sessionsQuery(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var sq SessionsQuery
	if err := r.Decode(&sq); err != nil {
		return nil, err
	}
	order, ok := tend.ParseSort(sq.Sort)
	if !ok {
		return nil, bad("sort " + sq.Sort)
	}
	limit := pageSize(sq.Limit, sessionsLimit)
	toks := tend.Tokens(sq.Q)
	q := tend.Parse(sq.Q)
	sent := withoutHost(sq.Q, toks)
	c.mu.Lock()
	ms := c.sessionMachines(p, q.Host, len(q.Words) > 0)
	c.mu.Unlock()
	profiles := c.Profiles()
	parts := make([]machineRows, len(ms))
	var wg sync.WaitGroup
	for i, s := range ms {
		wg.Go(func() { parts[i] = c.queryMachine(ctx, s, sq, sent, order, limit, profiles) })
	}
	wg.Wait()

	page := SessionsPage{Rows: []SessionRow{}, Machines: []MachineAnswer{}, Tokens: toks,
		Facets: SessionFacets{Facets: remote.Facets{Projects: map[string]int{}, Groups: map[string]int{}, Tags: map[string]int{},
			Providers: map[string]int{}}, Machines: map[string]int{}}}
	if q.Status == tend.StatusAgent {
		page.Status = q.Status
	}
	if page.Tokens == nil {
		page.Tokens = []tend.Token{}
	}
	type placed struct {
		row SessionRow
		at  tend.Cursor
	}
	var all []placed
	more := false
	for _, part := range parts {
		page.Machines = append(page.Machines, part.answer)
		if part.answer.State != AnswerOK && part.answer.State != AnswerOld {
			continue
		}
		page.Facets.Machines[part.answer.Name] = part.answer.Matched
		sumFacets(&page.Facets.Facets, part.facets)
		for i, row := range part.rows {
			all = append(all, placed{row, part.at[i]})
		}
		more = more || part.more
	}
	slices.SortStableFunc(all, func(a, b placed) int {
		if c := tend.Compare(order, a.at, b.at); c != 0 {
			return c
		}
		return strings.Compare(a.row.Machine, b.row.Machine)
	})
	if len(all) > limit {
		all, more = all[:limit], true
	}
	for _, x := range all {
		page.Rows = append(page.Rows, x.row)
	}
	if more && len(all) > 0 {
		last := all[len(all)-1]
		page.Next = &SessionsCursor{Cursor: last.at, Machine: last.row.Machine}
	}
	return page, nil
}

func sumFacets(into *remote.Facets, f remote.Facets) {
	for _, x := range []struct{ into, from map[string]int }{{into.Projects, f.Projects}, {into.Groups, f.Groups},
		{into.Tags, f.Tags}, {into.Providers, f.Providers}} {
		for k, n := range x.from {
			x.into[k] += n
		}
	}
}

// queryMachine is s's part of a sessions.query: its node's query, or, where its node has none, its whole list read
// with index.Select here.
func (c *Coord) queryMachine(ctx context.Context, s *sessionsOf, sq SessionsQuery, sent string, order tend.SortBy, limit int,
	profiles []tend.AgentProfile) machineRows {
	ctx, cancel := context.WithTimeout(ctx, machineWait)
	defer cancel()
	h, a, ok := c.reachFor(ctx, s)
	out := machineRows{answer: a}
	if !ok {
		return out
	}
	var after *remote.Cursor
	if sq.After != nil {
		after = new(sq.After.Cursor)
	}
	resumes := slices.Contains(h.Methods, node.MRunResume)
	var res remote.QueryResult
	var err error
	old := !slices.Contains(h.Methods, remote.MQuery)
	trash := tend.Parse(sent).Status == tend.StatusTrash
	if trash && !slices.Contains(h.Methods, remote.MTrash) {
		out.answer.State, out.answer.Writable, out.answer.Trash = AnswerOld, false, false
		return out
	}
	if old {
		res, err = c.selectOld(ctx, s, sent, sq.All, order, after, limit)
	} else {
		err = c.call(ctx, s.name, remote.MQuery, remote.QueryParams{Q: sent, All: sq.All, Sort: order.String(), Limit: limit,
			After: after, Projects: s.dirs, Also: s.also, Fresh: sq.Fresh}, &res)
	}
	if err != nil {
		c.failedAnswer(&out.answer, s, err)
		return out
	}
	out.answer.State = AnswerOK
	if old {
		out.answer.State, out.answer.Writable = AnswerOld, false
	}
	out.answer.Total, out.answer.Matched, out.answer.Running = res.Total, res.Matched, res.Running
	out.answer.TrashDays = res.TrashDays
	out.facets, out.more = res.Facets, res.Next != nil
	now := time.Now()
	for _, row := range res.Rows {
		sr := SessionRow{Machine: s.name, Writable: out.answer.Writable && !trash, Row: row}
		sr.Task = s.tasks[row.SessionID]
		if s.owned && !trash {
			sr.Make = s.makeTask(row, old || !resumes, profiles, now)
		}
		out.rows = append(out.rows, sr)
		out.at = append(out.at, order.Cursor(row.Rec("")))
	}
	return out
}

// makeTask is whether row can become a task now, as its machine's owner sees it; old: its machine cannot tell or
// cannot do it.
func (s *sessionsOf) makeTask(row remote.Row, old bool, profiles []tend.AgentProfile, now time.Time) *MakeTask {
	mk := &MakeTask{Agents: continueAgents(profiles, row.Provider, s.name)}
	switch {
	case old:
		mk.Why = MakeOld
	case row.Live != nil || s.busy[row.SessionID] ||
		row.Provider == tend.ProviderCodex && !row.LastAt.IsZero() && now.Sub(row.LastAt) < agent.CodexQuiet:
		mk.Why = MakeBusy
	case len(mk.Agents) == 0:
		mk.Why = MakeNoAgent
	}
	return mk
}

// continueAgents are the profiles that can go on with a session of provider on machine, the one named after the
// provider first.
func continueAgents(profiles []tend.AgentProfile, provider, machine string) []string {
	var out []string
	for _, p := range profiles {
		if agent.CanContinue(p, provider, machine) {
			out = append(out, p.Name)
		}
	}
	if i := slices.Index(out, provider); i > 0 {
		out = append([]string{provider}, slices.Delete(out, i, i+1)...)
	}
	return out
}

// selectOld answers query for a machine whose node has none: its whole list and who of it runs, read with the same
// index.Select a node's query uses. A failed live counts as none running.
func (c *Coord) selectOld(ctx context.Context, s *sessionsOf, q string, all bool, order tend.SortBy, after *remote.Cursor,
	limit int) (remote.QueryResult, error) {
	var list remote.List
	var live remote.Live
	done := make(chan struct{})
	go func() {
		defer close(done)
		if c.call(ctx, s.name, remote.MLive, nil, &live) != nil {
			live.Live = nil
		}
	}()
	err := c.call(ctx, s.name, remote.MList, nil, &list)
	<-done
	if err != nil {
		return remote.QueryResult{}, err
	}
	query := tend.Parse(q)
	if query.Status == tend.StatusAgent {
		return remote.QueryResult{Status: query.Status}, nil
	}
	query.All = all
	query.Live = func(id string) bool { _, ok := live.Live[id]; return ok }
	if s.also != nil {
		query.Also = func(r *tend.Rec) string { return s.also[r.SessionID] }
	}
	recs := make([]*tend.Rec, len(list.Sessions))
	for i, ss := range list.Sessions {
		recs[i] = ss.Rec("")
	}
	rows := &index.Rows{Belong: func(r *tend.Rec) (string, string) {
		if pr := task.ProjectOf(s.projects, s.name, s.goos, cmp.Or(r.Repo, r.Cwd)); pr != nil {
			return pr.ID, pr.Name
		}
		return "", ""
	}}
	sel := index.Select(rows, recs, query, index.Page{Sort: order, After: after, Limit: limit})
	res := remote.QueryResult{Rows: []remote.Row{}, Next: sel.Next, Total: sel.Total, Matched: sel.Matched, Running: sel.Running,
		Facets: sel.Facets}
	for _, r := range sel.Rows {
		row := remote.Row{Session: remote.SessionOf(r), Project: r.ProjectID}
		if l, ok := live.Live[r.SessionID]; ok {
			row.Live = new(l)
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// machineHits is one machine's part of a sessions.grep.
type machineHits struct {
	answer  MachineAnswer
	hits    []SessionHit
	fixes   []string
	tooLong bool
}

func (c *Coord) sessionsGrep(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var sg SessionsGrep
	if err := r.Decode(&sg); err != nil {
		return nil, err
	}
	limit := pageSize(sg.Limit, grepLimit)
	toks := tend.Tokens(sg.Q)
	q := tend.Parse(sg.Q)
	sent := withoutHost(sg.Q, toks)
	c.mu.Lock()
	ms := c.sessionMachines(p, q.Host, true)
	c.mu.Unlock()
	parts := make([]machineHits, len(ms))
	var wg sync.WaitGroup
	for i, s := range ms {
		wg.Go(func() { parts[i] = c.grepMachine(ctx, s, sg, sent, limit) })
	}
	wg.Wait()
	found := SessionsFound{Hits: []SessionHit{}, Machines: []MachineAnswer{}}
	var lists [][]SessionHit
	for _, part := range parts {
		found.Machines = append(found.Machines, part.answer)
		lists = append(lists, part.hits)
		for _, f := range part.fixes {
			if !slices.Contains(found.Fixes, f) {
				found.Fixes = append(found.Fixes, f)
			}
		}
		found.TooLong = found.TooLong || part.tooLong
	}
	found.Hits = remote.Interleave(lists, func(h SessionHit) bool { return h.AllInOne }, limit)
	return found, nil
}

// grepMachine is s's part of a sessions.grep; a node without grep is not searched.
func (c *Coord) grepMachine(ctx context.Context, s *sessionsOf, sg SessionsGrep, sent string, limit int) machineHits {
	ctx, cancel := context.WithTimeout(ctx, machineWait)
	defer cancel()
	h, a, ok := c.reachFor(ctx, s)
	out := machineHits{answer: a}
	if !ok {
		return out
	}
	if !slices.Contains(h.Methods, remote.MGrep) {
		out.answer.State, out.answer.Writable = AnswerOld, false
		return out
	}
	var res remote.GrepResult
	err := c.call(ctx, s.name, remote.MGrep, remote.GrepParams{Q: sent, All: sg.All, Limit: limit, BudgetMS: int(remote.GrepBudget / time.Millisecond),
		Projects: s.dirs, Also: s.also}, &res)
	if err != nil {
		c.failedAnswer(&out.answer, s, err)
		return out
	}
	out.answer.State, out.answer.Building = AnswerOK, res.Building
	out.answer.Matched = len(res.Hits)
	out.fixes, out.tooLong = res.Fixes, res.TooLong
	for _, hit := range res.Hits {
		out.hits = append(out.hits, SessionHit{Machine: s.name, GrepHit: hit})
	}
	return out
}

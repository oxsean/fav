package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// fakeSessions answers the sessions page's calls as the coordinator and its nodes would, from made-up sessions held
// in memory: sessions.query (status:trash too) and sessions.grep over the machines the viewer reads, node.call's put,
// trash, restore, messages, hits and text, and people.names. Ann (u_a) owns mba, linux (an older tend, without a
// trash), win (offline) and slow (too late to answer); Bo (u_b) owns bo-laptop, which shows Ann only its runs'
// sessions, and reads mba, which Ann shares with him. mba and bo-laptop have trashes, each with a session in it; the
// running session on mba refuses to be deleted. mba builds its message store for the first searches. Queries are read
// by tend.Parse and index.Select, writes applied by tend.Patch, as on a node; message search is plain substring
// matching.
type fakeSessions struct {
	mu       sync.Mutex
	start    time.Time
	at       time.Time
	machines []fakeMachine
	sessions []*fakeSession
	greps    int
	revoked  map[string]bool // machines whose readers previewRevoke took the viewer from
}

type fakeMachine struct {
	name, owner, state string
	share              string // what it shows others: all | runs
	readers            []string
	since              *time.Time
	trash              bool // its node has trash and restore
	trashDays          int
}

type fakeSession struct {
	machine string
	rec     *tend.Rec
	msgs    []capture.Message // oldest first, each whole
	live    *capture.Live
	task    *coord.TaskLink
	make    *coord.MakeTask
	ofRun   bool
	deleted *time.Time // in its machine's trash since
}

// ⚠️ The node keeps this much of a message's text in a page (the rest comes from text); the preview's people.
const shownChars = 600

var fakePeople = map[string]string{"u_a": "Ann Lee", "u_b": "Bo Lin", "u_c": "Cy Park", "u_d": "Dee Ray"}

func newFakeSessions(now time.Time) *fakeSessions {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	at := func(d time.Duration) *time.Time { t := ago(d); return &t }
	f := &fakeSessions{start: time.Now(), at: now, machines: []fakeMachine{
		{name: "mba", owner: "u_a", state: coord.AnswerOK, share: "all", readers: []string{"u_b"}, trash: true, trashDays: 30},
		{name: "linux", owner: "u_a", state: coord.AnswerOld, share: "all"},
		{name: "win", owner: "u_a", state: coord.AnswerOffline, share: "runs", since: at(3*time.Hour + 30*time.Minute)},
		{name: "slow", owner: "u_a", state: coord.AnswerTimeout, share: "all"},
		{name: "bo-laptop", owner: "u_b", state: coord.AnswerOK, share: "runs", readers: []string{"u_a"}, trash: true, trashDays: 14},
	}}
	add := func(machine, provider, sid, title, cwd string, last time.Duration, s fakeSession, r tend.Rec, talk ...string) {
		r.Provider, r.SessionID, r.Title, r.Cwd = provider, sid, title, cwd
		started := ago(last + time.Duration(len(talk))*7*time.Minute)
		r.SessionStartedAt = &started
		if r.FavoritedAt != nil {
			r.ID = "r-" + sid
		}
		if r.UpdatedAt.IsZero() {
			r.UpdatedAt = ago(last)
		}
		for i, t := range talk {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			when := started.Add(time.Duration(i) * 7 * time.Minute)
			if i == len(talk)-1 {
				when = ago(last)
			}
			s.msgs = append(s.msgs, capture.Message{Role: role, Text: t, Chars: utf8.RuneCountInString(t), Off: int64(400 * (i + 1)), At: when})
		}
		r.Attach((len(talk)+1)/2, len(talk), ago(last), "")
		s.machine, s.rec = machine, &r
		f.sessions = append(f.sessions, &s)
	}
	long := strings.Repeat("The notes group the merged PRs by area: the importer, the checkout, the docs site and the CI cache. ", 16)
	add("mba", "claude", "c-fix", "Fix the checkout total", "/Users/ann/dev/shop", 12*time.Minute,
		fakeSession{live: &capture.Live{Status: "working", Agent: "claude", Since: ago(40 * time.Minute)}, task: &coord.TaskLink{ID: "t2", Title: "Cart totals"},
			make: &coord.MakeTask{Why: coord.MakeBusy, Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(5 * time.Hour), Tags: []string{"checkout", "bug"}, Status: tend.StatusDoing, GitBranch: "fix-total", ProjectID: "p1", ProjectName: "Shop",
			Summary: "The checkout total is off by a cent: each line is rounded before the sum."},
		"Why is the checkout total off by a cent?", "Checkout rounds each line before it sums the total, so three lines of 0.333 lose a cent.",
		"Sum first, then round.", "The total now sums before checkout rounds; I added a test with three lines of 0.333.",
		"Run the cart tests too.", "All 48 cart tests pass. The checkout total matches the invoice now.")
	add("mba", "claude", "c-notes", "Draft the release notes", "/Users/ann/dev/shop", 2*time.Hour,
		fakeSession{make: &coord.MakeTask{Agents: []string{"claude", "codex"}}},
		tend.Rec{FavoritedAt: at(3 * time.Hour), Tags: []string{"release", "docs"}, Status: tend.StatusDoing, GitBranch: "notes", ProjectID: "p1", ProjectName: "Shop",
			Summary: "Group the merged PRs since v0.9 by area and draft the notes; waits on the importer change."},
		"Draft the release notes for v1.0 from the merged PRs.", long, "Put the importer first.", "Done: the importer leads, then the checkout fix and the docs.")
	add("mba", "codex", "x-port", "Port the importer", "/Users/ann/dev/my shop", 5*time.Hour,
		fakeSession{make: &coord.MakeTask{Agents: []string{"codex"}}},
		tend.Rec{FavoritedAt: at(6 * time.Hour), Tags: []string{"importer", "csv", "perf"},
			Summary: "CSV 导入改成流式读取，内存从 1.2 GB 降到 80 MB；Windows 换行还没测。"},
		"把 CSV 导入改成流式读取，别一次读进内存。", "改好了：importer 现在按行读，checkout 的 CSV 也走同一条路，内存从 1.2 GB 降到 80 MB。",
		"Windows 换行呢？", "还没测 CRLF；下一步给 importer 加一个 Windows 换行的测试。")
	add("mba", "claude", "c-readme", "Tidy the README", "/Users/ann/dev/shop", 26*time.Hour, fakeSession{make: &coord.MakeTask{Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(30 * time.Hour), ArchivedAt: at(25 * time.Hour), Tags: []string{"docs"}, Summary: "Rewrote the install section."},
		"Tidy the README's install section.", "Rewrote it: one command per platform, and the checkout of the repo first.")
	add("mba", "claude", "c-rate", "Try the new rate limiter", "/Users/ann/dev/api", 50*time.Minute, fakeSession{make: &coord.MakeTask{Agents: []string{"claude"}}},
		tend.Rec{}, "Try the token bucket rate limiter on the API.", "It holds at 200 requests a second; the total latency rises 3 ms.",
		"Keep it behind a flag.", "Done: RATE_LIMIT=1 turns it on.", "And a burst of 50?", "Bursts of 50 pass; the 51st waits 5 ms.")
	add("mba", "codex", "x-ci", "Speed up the CI cache", "/Users/ann/dev/shop", 28*time.Hour, fakeSession{make: &coord.MakeTask{Agents: []string{"codex"}}},
		tend.Rec{}, "The CI spends four minutes restoring its cache.", "The cache key hashes every lockfile; keyed by the Go one alone it restores in 40 s.",
		"Do it.", "Changed; the checkout step is the slowest now.", "Shallow checkout?", "fetch-depth 1 takes it from 50 s to 6 s.")
	add("linux", "claude", "c-photos", "Back up the photos", "/home/ann/photos", 7*time.Hour, fakeSession{make: &coord.MakeTask{Why: coord.MakeOld, Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(8 * time.Hour), Tags: []string{"backup"}},
		"Back the photos up to the NAS every night.", "A cron job at 02:00 runs rsync; the total is 412 GB.")
	add("linux", "claude", "c-logs", "Rotate the logs", "/var/log", 30*time.Hour, fakeSession{make: &coord.MakeTask{Why: coord.MakeOld, Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(31 * time.Hour), Tags: []string{"ops"}, Status: tend.StatusDone},
		"Rotate the logs weekly.", "logrotate keeps four weeks now.")
	add("win", "claude", "c-win", "Build the installer", `C:\Users\Ann\dev\shop`, 4*time.Hour, fakeSession{},
		tend.Rec{FavoritedAt: at(5 * time.Hour)}, "Build the MSI.", "Built.")
	add("bo-laptop", "claude", "c-search", "Add the search box", "/Users/bo/work/docs", 3*time.Hour, fakeSession{ofRun: true, make: &coord.MakeTask{Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(4 * time.Hour), Tags: []string{"docs", "search"}, Summary: "Search box on the docs site, with tests."},
		"Add a search box to the docs site.", "The search box is in, with tests; it searches titles and the checkout guide too.")
	add("bo-laptop", "claude", "c-hiring", "Notes on the hiring loop", "/Users/bo/notes", 9*time.Hour, fakeSession{make: &coord.MakeTask{Agents: []string{"claude"}}},
		tend.Rec{FavoritedAt: at(10 * time.Hour)}, "Summarize the hiring loop notes.", "Four stages; the take-home takes too long.")
	add("mba", "claude", "c-regex", "Scratch: regex for log lines", "/Users/ann/scratch", 17*24*time.Hour, fakeSession{deleted: at(16 * 24 * time.Hour)},
		tend.Rec{}, "A regex for the log lines' timestamps.", `^\d{4}-\d{2}-\d{2}T\S+ matches them; the zone is optional.`)
	add("mba", "claude", "c-sandbox", "Try the old payment sandbox", "/Users/ann/dev/shop", 30*time.Hour, fakeSession{deleted: at(20 * time.Hour)},
		tend.Rec{Tags: []string{"checkout"}}, "Does the old payment sandbox still answer?", "It answers, but its test cards expired; the new one replaces it.")
	add("bo-laptop", "claude", "c-draft", "Draft the offsite agenda", "/Users/bo/notes", 50*time.Hour, fakeSession{deleted: at(48 * time.Hour)},
		tend.Rec{}, "Draft the offsite agenda.", "Two days: planning, then the docs sprint.")
	return f
}

func (f *fakeSessions) now() time.Time { return f.at.Add(time.Since(f.start)).Truncate(time.Second) }

func (f *fakeSessions) machine(name string) *fakeMachine {
	for i := range f.machines {
		if f.machines[i].name == name {
			return &f.machines[i]
		}
	}
	return nil
}

func (m *fakeMachine) reads(who string) bool {
	return m.owner == who || slices.Contains(m.readers, who)
}

// shown are m's sessions who sees, in its trash or not: a machine sharing only its runs' sessions shows others those
// alone.
func (f *fakeSessions) shown(m *fakeMachine, who string, trashed bool) []*fakeSession {
	var out []*fakeSession
	for _, s := range f.sessions {
		if s.machine == m.name && (s.deleted != nil) == trashed && (m.owner == who || m.share == "all" || s.ofRun) {
			out = append(out, s)
		}
	}
	return out
}

func (f *fakeSessions) answer(m *fakeMachine, who string) coord.MachineAnswer {
	a := coord.MachineAnswer{Name: m.name, Owner: m.owner, State: m.state, Since: m.since}
	if m.owner == who && m.state == coord.AnswerOK {
		a.Writable, a.Trash = true, m.trash
	}
	if m.state == coord.AnswerOK {
		a.TrashDays = m.trashDays
	}
	if m.owner != who && m.share != "all" {
		a.Share = m.share
	}
	return a
}

func (f *fakeSessions) row(s *fakeSession, who string) coord.SessionRow {
	m := f.machine(s.machine)
	r := coord.SessionRow{Machine: s.machine, Row: remote.Row{Session: remote.SessionOf(s.rec), Project: s.rec.ProjectID, Live: s.live, DeletedAt: s.deleted}}
	if m.owner == who && s.deleted == nil {
		r.Writable, r.Make = m.state == coord.AnswerOK, s.make
		r.Task = s.task
	}
	return r
}

func (f *fakeSessions) query(who string, p coord.SessionsQuery) (coord.SessionsPage, error) {
	q := tend.Parse(p.Q)
	if q.Status == tend.StatusTrash {
		return f.trashed(who, p, q)
	}
	q.All = p.All
	host := q.Host
	if host == tend.HostAll || host == tend.HostLocal {
		host = ""
	}
	q.Host = ""
	running := map[string]bool{}
	for _, s := range f.sessions {
		if s.live != nil {
			running[s.rec.SessionID] = true
		}
	}
	q.Live = func(id string) bool { return running[id] }
	sortBy, _ := tend.ParseSort(p.Sort)
	out := coord.SessionsPage{Rows: []coord.SessionRow{}, Machines: []coord.MachineAnswer{}, Tokens: tend.Tokens(p.Q),
		Facets: coord.SessionFacets{Facets: remote.Facets{Projects: map[string]int{}, Tags: map[string]int{}, Providers: map[string]int{}}, Machines: map[string]int{}}}
	var picked []*tend.Rec
	of := map[*tend.Rec]*fakeSession{}
	for i := range f.machines {
		m := &f.machines[i]
		if !m.reads(who) || host != "" && !strings.EqualFold(host, m.name) {
			continue
		}
		a := f.answer(m, who)
		if m.state == coord.AnswerOK || m.state == coord.AnswerOld {
			var recs []*tend.Rec
			for _, s := range f.shown(m, who, false) {
				recs = append(recs, s.rec)
				of[s.rec] = s
			}
			sel := index.Select(nil, recs, q, index.Page{Sort: sortBy})
			a.Total, a.Matched, a.Running = sel.Total, sel.Matched, sel.Running
			picked = append(picked, sel.Rows...)
			for k, n := range sel.Facets.Projects {
				out.Facets.Projects[k] += n
			}
			for k, n := range sel.Facets.Tags {
				out.Facets.Tags[k] += n
			}
			for k, n := range sel.Facets.Providers {
				out.Facets.Providers[k] += n
			}
			if sel.Matched > 0 {
				out.Facets.Machines[m.name] = sel.Matched
			}
		}
		out.Machines = append(out.Machines, a)
	}
	sortBy.Sort(picked)
	from := 0
	if p.After != nil {
		from = sort.Search(len(picked), func(i int) bool { return tend.Less(sortBy, p.After.Cursor, sortBy.Cursor(picked[i])) })
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}
	to := min(len(picked), from+limit)
	for _, r := range picked[from:to] {
		out.Rows = append(out.Rows, f.row(of[r], who))
	}
	if to < len(picked) {
		last := picked[to-1]
		out.Next = &coord.SessionsCursor{Cursor: sortBy.Cursor(last), Machine: of[last].machine}
	}
	return out, nil
}

// trashed answers status:trash: each machine's trash, newest deletion first, every row read only; a machine without
// trash is old, as the coordinator says of a node without the method.
func (f *fakeSessions) trashed(who string, p coord.SessionsQuery, q tend.Query) (coord.SessionsPage, error) {
	host := q.Host
	if host == tend.HostAll || host == tend.HostLocal {
		host = ""
	}
	q.Host, q.Status, q.All, q.Turns = "", "all", p.All, 0
	out := coord.SessionsPage{Rows: []coord.SessionRow{}, Machines: []coord.MachineAnswer{}, Tokens: tend.Tokens(p.Q),
		Facets: coord.SessionFacets{Facets: remote.Facets{Projects: map[string]int{}, Tags: map[string]int{}, Providers: map[string]int{}}, Machines: map[string]int{}}}
	var picked []*fakeSession
	for i := range f.machines {
		m := &f.machines[i]
		if !m.reads(who) || host != "" && !strings.EqualFold(host, m.name) {
			continue
		}
		a := f.answer(m, who)
		if m.state == coord.AnswerOld || m.state == coord.AnswerOK && !m.trash {
			a.State, a.Writable, a.Trash = coord.AnswerOld, false, false
		}
		if a.State == coord.AnswerOK {
			for _, s := range f.shown(m, who, true) {
				a.Total++
				if q.Match(s.rec) {
					a.Matched++
					picked = append(picked, s)
					out.Facets.Machines[m.name]++
					out.Facets.Projects[s.rec.ProjectID]++
					out.Facets.Providers[s.rec.Provider]++
					for _, t := range s.rec.Tags {
						out.Facets.Tags[t]++
					}
				}
			}
		}
		out.Machines = append(out.Machines, a)
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].deleted.After(*picked[j].deleted) })
	for _, s := range picked {
		out.Rows = append(out.Rows, f.row(s, who))
	}
	return out, nil
}

// words are the keywords of a message search, lowercased; a word no message has is fixed to one a message has, one
// letter away.
func (f *fakeSessions) words(keywords string) (words, fixes []string) {
	known := map[string]bool{}
	for _, s := range f.sessions {
		for _, m := range s.msgs {
			for _, w := range strings.FieldsFunc(strings.ToLower(m.Text), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127) }) {
				known[w] = true
			}
		}
	}
	for _, w := range strings.Fields(strings.ToLower(strings.ReplaceAll(keywords, `"`, ""))) {
		if strings.HasPrefix(w, "-") || strings.Contains(w, ":") {
			continue
		}
		if !f.anywhere(w) {
			for k := range known {
				if oneOff(w, k) {
					fixes = append(fixes, k)
					w = k
					break
				}
			}
		}
		words = append(words, w)
	}
	return words, fixes
}

func (f *fakeSessions) anywhere(w string) bool {
	for _, s := range f.sessions {
		for _, m := range s.msgs {
			if strings.Contains(strings.ToLower(m.Text), w) {
				return true
			}
		}
	}
	return false
}

// oneOff: b is a with one letter left out, added or changed.
func oneOff(a, b string) bool {
	if a == b || len(a)-len(b) > 1 || len(b)-len(a) > 1 {
		return false
	}
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[min(i+1, len(a)):] == b[min(i+1, len(b)):] || a[i:] == b[min(i+1, len(b)):] || a[min(i+1, len(a)):] == b[i:]
}

// spans are the byte ranges of words in text, in order.
func spans(text string, words []string) [][2]int {
	low := strings.ToLower(text)
	var out [][2]int
	for _, w := range words {
		for at := 0; ; {
			i := strings.Index(low[at:], w)
			if i < 0 || w == "" {
				break
			}
			out = append(out, [2]int{at + i, at + i + len(w)})
			at += i + len(w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// ⚠️ How much of a message a hit's snippet shows, in runes, and how much of it comes before the first word.
const snipRunes, snipBefore = 120, 30

// snippet is the part of text around its first word.
func snippet(text string, words []string) string {
	r := []rune(text)
	if len(r) <= snipRunes {
		return text
	}
	from := 0
	if sp := spans(text, words); len(sp) > 0 {
		from = max(0, utf8.RuneCountInString(text[:sp[0][0]])-snipBefore)
	}
	from = min(from, len(r)-snipRunes)
	return string(r[from : from+snipRunes])
}

func has(text string, words []string, all bool) bool {
	low := strings.ToLower(text)
	n := 0
	for _, w := range words {
		if strings.Contains(low, w) {
			n++
		}
	}
	return n > 0 && (!all || n == len(words))
}

func (f *fakeSessions) grep(who string, p coord.SessionsGrep) (coord.SessionsFound, error) {
	keywords, scope := fulltext.Split(p.Q)
	words, fixes := f.words(keywords)
	out := coord.SessionsFound{Hits: []coord.SessionHit{}, Machines: []coord.MachineAnswer{}, Fixes: fixes}
	if fulltext.TooLong(keywords) {
		out.TooLong, out.Fixes = true, nil
		words = nil
	}
	q := tend.Parse(scope)
	q.All, q.Host = p.All, ""
	f.greps++
	for i := range f.machines {
		m := &f.machines[i]
		if !m.reads(who) {
			continue
		}
		a := f.answer(m, who)
		if m.state == coord.AnswerOK && m.name == "mba" && f.greps <= 2 {
			a.Building = &remote.Progress{Done: 120 * f.greps, Total: 400}
		}
		out.Machines = append(out.Machines, a)
		if m.state != coord.AnswerOK || len(words) == 0 {
			continue
		}
		for _, s := range f.shown(m, who, false) {
			if !q.Match(s.rec) {
				continue
			}
			row := f.row(s, who)
			hit := coord.SessionHit{Machine: m.name, Make: row.Make, GrepHit: remote.GrepHit{Row: row.Row, File: fileOf(s)}}
			for _, msg := range s.msgs {
				one := has(msg.Text, words, true)
				if !one && !has(msg.Text, words, false) {
					continue
				}
				hit.Hits++
				if hit.Snippet == "" || one && !hit.AllInOne {
					snip := snippet(msg.Text, words)
					hit.AllInOne, hit.Snippet, hit.Spans, hit.Off, hit.At = one, snip, spans(snip, words), msg.Off, msg.At
				}
				hit.Latest = msg.At
			}
			if hit.Hits > 0 {
				out.Hits = append(out.Hits, hit)
			}
		}
	}
	sort.SliceStable(out.Hits, func(i, j int) bool {
		a, b := out.Hits[i], out.Hits[j]
		if a.AllInOne != b.AllInOne {
			return a.AllInOne
		}
		return a.Hits > b.Hits
	})
	return out, nil
}

func fileOf(s *fakeSession) string { return "f-" + s.rec.SessionID }

// session is the session ref on machine who reads, in its trash (trashed) or not; write: one who may change, through a
// node that has method.
func (f *fakeSessions) session(who, machine string, ref remote.Ref, write bool, trashed ...bool) (*fakeSession, error) {
	m := f.machine(machine)
	if m == nil || !m.reads(who) || write && m.owner != who {
		return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: coord.MNodeCall}
	}
	if m.state == coord.AnswerOffline || m.state == coord.AnswerTimeout {
		return nil, &wire.Error{Code: wire.CodeOffline, Detail: machine}
	}
	in := [][]*fakeSession{f.shown(m, who, false), f.shown(m, who, true)}
	if len(trashed) > 0 {
		in = [][]*fakeSession{f.shown(m, who, trashed[0])}
		if !m.trash || m.state != coord.AnswerOK {
			return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: remote.MTrash}
		}
	}
	for _, s := range slices.Concat(in...) {
		if s.rec.Provider == ref.Provider && s.rec.SessionID == ref.SessionID {
			if write && m.state != coord.AnswerOK {
				return nil, &wire.Error{Code: wire.CodeUnknownMethod, Detail: remote.MPut}
			}
			return s, nil
		}
	}
	return nil, &wire.Error{Code: wire.CodeNotFound}
}

func (f *fakeSessions) node(who string, call coord.NodeCall) (any, error) {
	switch call.Method {
	case remote.MPut:
		var p remote.PutParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, err
		}
		s, err := f.session(who, call.Machine, p.Ref, true, false)
		if err != nil {
			return nil, err
		}
		if p.Expect != nil && !p.Expect.Equal(s.rec.UpdatedAt) {
			return nil, &wire.Error{Code: wire.CodeStale, Detail: "the record changed"}
		}
		now := f.now()
		p.Patch.Apply(s.rec, now)
		if s.rec.ID == "" {
			s.rec.ID = "r-" + s.rec.SessionID
		}
		s.rec.UpdatedAt = now
		s.rec.Prepare()
		return remote.Row{Session: remote.SessionOf(s.rec), Live: s.live}, nil
	case remote.MTrash, remote.MRestore:
		var p remote.TrashParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, err
		}
		restore := call.Method == remote.MRestore
		s, err := f.session(who, call.Machine, p.Ref, true, restore)
		if err != nil {
			return nil, err
		}
		if !restore && s.live != nil {
			return nil, &wire.Error{Code: wire.CodeBusy, Detail: "the session is running"}
		}
		if restore {
			s.deleted = nil
			return remote.RestoreResult{Title: s.rec.Title, Files: 1}, nil
		}
		now := f.now()
		s.deleted = &now
		return remote.TrashResult{Title: s.rec.Title, Files: 1}, nil
	case remote.MMessages:
		var p remote.MessagesParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, err
		}
		s, err := f.session(who, call.Machine, p.Ref, false)
		if err != nil {
			return nil, err
		}
		if p.File != "" && p.File != fileOf(s) {
			return nil, &wire.Error{Code: wire.CodeStale, Detail: "file"}
		}
		words, _ := f.words(p.Find)
		page := capture.Page{File: fileOf(s), Done: true}
		for i := len(s.msgs) - 1; i >= 0 && len(page.Msgs) < max(1, p.N); i-- {
			m := s.msgs[i]
			if p.Before >= 0 && m.Off >= p.Before {
				continue
			}
			if r := []rune(m.Text); len(r) > shownChars {
				m.Text = string(r[:shownChars])
			}
			if len(words) > 0 {
				m.Spans = spans(m.Text, words)
			}
			page.Msgs = append(page.Msgs, m)
			page.From, page.Done = m.Off, i == 0
		}
		return page, nil
	case remote.MHits:
		var p remote.HitsParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, err
		}
		s, err := f.session(who, call.Machine, p.Ref, false)
		if err != nil {
			return nil, err
		}
		words, _ := f.words(p.Q)
		out := remote.HitsResult{Hits: []remote.Hit{}}
		for i := len(s.msgs) - 1; i >= 0; i-- {
			if m := s.msgs[i]; has(m.Text, words, false) {
				out.Total++
				out.Hits = append(out.Hits, remote.Hit{Off: m.Off, Role: m.Role, At: m.At, Text: m.Text, Spans: spans(m.Text, words), File: fileOf(s)})
			}
		}
		return out, nil
	case remote.MText:
		var p remote.TextParams
		if err := json.Unmarshal(call.Params, &p); err != nil {
			return nil, err
		}
		s, err := f.session(who, call.Machine, p.Ref, false)
		if err != nil {
			return nil, err
		}
		for _, m := range s.msgs {
			if m.Off == p.Off {
				return remote.Text{Text: m.Text}, nil
			}
		}
		return nil, &wire.Error{Code: wire.CodeNotFound}
	}
	return nil, &wire.Error{Code: wire.CodeUnauthorized, Detail: call.Method}
}

// serve answers POST /fake/call?as=<member|admin> with {method, params}: {result} or {error}.
func (f *fakeSessions) serve(w http.ResponseWriter, r *http.Request) {
	who := "u_b"
	if r.URL.Query().Get("as") == "admin" {
		who = "u_a"
	}
	var in struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	result, err := f.call(who, in.Method, in.Params)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var we *wire.Error
	switch {
	case errors.As(err, &we):
		json.NewEncoder(w).Encode(map[string]any{"error": we})
	case err != nil:
		json.NewEncoder(w).Encode(map[string]any{"error": wire.Error{Code: wire.CodeBadRequest, Detail: err.Error()}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"result": result})
	}
}

// previewRevoke is the preview's own call (webtest/preview's previewReset): the machines shared with the viewer stop
// sharing their sessions with them, or share them again.
const previewRevoke = "preview.revoke"

func (f *fakeSessions) revoke(who string) {
	if f.revoked == nil {
		f.revoked = map[string]bool{}
	}
	for i := range f.machines {
		m := &f.machines[i]
		switch {
		case m.owner == who:
		case slices.Contains(m.readers, who):
			m.readers = slices.DeleteFunc(m.readers, func(r string) bool { return r == who })
			f.revoked[m.name] = true
		case f.revoked[m.name]:
			m.readers = append(m.readers, who)
			delete(f.revoked, m.name)
		}
	}
}

func (f *fakeSessions) call(who, method string, params json.RawMessage) (any, error) {
	switch method {
	case coord.MSessionsQuery:
		var p coord.SessionsQuery
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return f.query(who, p)
	case coord.MSessionsGrep:
		var p coord.SessionsGrep
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return f.grep(who, p)
	case coord.MPeopleNames:
		var p coord.PeopleParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		out := coord.People{Names: map[string]string{}}
		for _, id := range p.IDs {
			if n := fakePeople[id]; n != "" {
				out.Names[id] = n
			}
		}
		return out, nil
	case coord.MNodeCall:
		var p coord.NodeCall
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return f.node(who, p)
	case previewRevoke:
		f.revoke(who)
		return struct{}{}, nil
	}
	return nil, fmt.Errorf("webpreview: no fake answer for %s", method)
}

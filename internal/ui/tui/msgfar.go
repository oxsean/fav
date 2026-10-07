package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
)

// Message search on the other machines the list shows: each machine's grep through m.hosts (ssh in mode 1, node.call in
// mode 2), one command per machine, so each machine's hits join the list when it answers. Every search reaches every
// machine, so it goes after a pause in typing or when the box is left, not on every keystroke.

const farPause = 600 * time.Millisecond

type farSearch struct {
	want     string // search asked for: view, query and machines
	key      string // search sent; "" while the pause runs
	seq      int
	ctx      context.Context
	cancel   context.CancelFunc
	typed    bool     // the box had focus at the last look
	machines []string // searched, in the order of m.hosts.Names
	answers  map[string]*farAnswer
	rows     map[string]*tend.Rec // msgKey → row of a hit, the listed row when there is one
}

type farAnswer struct {
	asking   bool
	items    []msgItem
	err      error
	building *remote.Progress
	fixes    []string
}

// incomplete: the machine did not answer, or answered while its text store was still being built.
func (a *farAnswer) incomplete() bool { return !a.asking && (a.err != nil || a.building != nil) }

type farTickMsg int

type farGrepMsg struct {
	seq int
	key string
	g   remote.Grepped
}

// farMachines are the other machines the list shows now: the host: filter picks them; trash and one-shot agent runs are
// this machine's only.
func (m *Model) farMachines() []string {
	if m.hosts == nil || m.view == viewLive {
		return nil
	}
	q := m.query()
	if q.Status == tend.StatusTrash || q.Status == tend.StatusAgent {
		return nil
	}
	var out []string
	for _, name := range m.hosts.Names() {
		if m.remote[name] != nil && hostSelected(q, name) {
			out = append(out, name)
		}
	}
	return out
}

func (m *Model) issueFarSearch() tea.Cmd {
	f := &m.msg.far
	left := f.typed && !m.typing
	f.typed = m.typing
	var names []string
	key := ""
	if kw := m.msgKeywords(); kw != "" && !fulltext.TooLong(kw) {
		if names = m.farMachines(); len(names) > 0 {
			q, _ := m.msgQuery()
			key = strings.Join(append([]string{strconv.Itoa(int(m.view)), q}, names...), "\x00")
		}
	}
	if key != f.want {
		if f.cancel != nil {
			f.cancel()
		}
		f.seq++
		*f = farSearch{want: key, seq: f.seq, typed: f.typed, answers: map[string]*farAnswer{}}
		if key != "" {
			f.machines = names
		}
		if m.msg.res != nil { // the last query's hits from other machines go now, not when this machine's next result comes
			m.mergeMsg()
			m.refresh()
		}
		if key == "" {
			return nil
		}
		if m.typing {
			seq := f.seq
			return tea.Tick(farPause, func(time.Time) tea.Msg { return farTickMsg(seq) })
		}
		return m.runFarSearch()
	}
	if left && key != "" && (f.key == "" || slices.ContainsFunc(f.machines, func(n string) bool {
		a := f.answers[n]
		return a != nil && a.incomplete()
	})) {
		return m.runFarSearch() // leaving the box sends it now, or asks again those that did not answer in full
	}
	return nil
}

func (msg farTickMsg) apply(m *Model) tea.Cmd {
	if int(msg) != m.msg.far.seq || m.msg.far.key != "" {
		return nil
	}
	return m.runFarSearch()
}

// runFarSearch asks each machine without a full answer to this search; a machine known to be down is not asked.
func (m *Model) runFarSearch() tea.Cmd {
	f := &m.msg.far
	if f.want == "" {
		return nil
	}
	if f.ctx == nil {
		f.ctx, f.cancel = context.WithCancel(context.Background())
	}
	f.key = f.want
	q, _ := m.msgQuery()
	var cmds []tea.Cmd
	for _, name := range f.machines {
		a := f.answers[name]
		if a != nil && (a.asking || !a.incomplete()) {
			continue
		}
		if a == nil {
			a = &farAnswer{}
			f.answers[name] = a
		}
		if err := m.farDown(name); err != nil {
			a.err = err
			continue
		}
		a.asking = true
		p := remote.GrepParams{Q: q, All: m.view != viewFavorites, Limit: remote.GrepLimit, BudgetMS: int(remote.GrepBudget / time.Millisecond)}
		if m.proj.snap.Ready() {
			p.Projects = remote.ProjectDirsOn(m.proj.snap.Projects, name)
		}
		h, ctx, seq, key := m.hosts, f.ctx, f.seq, f.key
		cmds = append(cmds, func() tea.Msg {
			g := h.GrepWithin(ctx, name, p, remote.GrepWait)
			if ctx.Err() != nil {
				return nil
			}
			return farGrepMsg{seq, key, g}
		})
	}
	return tea.Batch(cmds...)
}

// farDown is why name is not asked: the server is down, or the list's last fetch from it failed.
func (m *Model) farDown(name string) error {
	if err := m.serverDown(); err != nil {
		return err
	}
	if hr := m.remote[name]; hr != nil && hr.err != nil {
		return hr.err
	}
	return nil
}

func (msg farGrepMsg) apply(m *Model) tea.Cmd {
	f := &m.msg.far
	a := f.answers[msg.g.Machine]
	if msg.seq != f.seq || msg.key != f.key || a == nil {
		return nil
	}
	name, res, had := msg.g.Machine, msg.g.Result, a.items
	*a = farAnswer{err: msg.g.Err}
	if a.err != nil { // what an earlier answer to this search found stays listed
		a.items = had
		return m.showMsgResults()
	}
	a.building, a.fixes = res.Building, res.Fixes
	if f.rows == nil {
		f.rows = map[string]*tend.Rec{}
	}
	for _, hit := range res.Hits {
		r := m.farRow(name, hit.Row.Session)
		a.items = append(a.items, msgItem{msgKey(r), r, found{file: hit.File, Result: fulltext.Result{Hits: hit.Hits,
			AllInOne: hit.AllInOne, Snippet: hit.Snippet, Off: hit.Off, At: hit.At.Local(), Latest: hit.Latest.Local()}}})
	}
	return m.showMsgResults()
}

// farRow is the row of s on name: the listed one when the list has it (cursor, pin and probes key by pointer), else
// one kept for this search's hits, reused across answers.
func (m *Model) farRow(name string, s remote.Session) *tend.Rec {
	fresh := s.Rec(name)
	k := msgKey(fresh)
	if hr := m.remote[name]; hr != nil {
		if r := hr.byKey[fresh.Key()]; r != nil {
			m.msg.far.rows[k] = r
			return r
		}
	}
	if r := m.msg.far.rows[k]; r != nil {
		*r = *fresh
		return r
	}
	m.msg.far.rows[k] = fresh
	return fresh
}

// msgFixes are the spellings searched besides the keywords, here and on the machines that answered.
func (m *Model) msgFixes() []string {
	fixes := queryOf(m.msgKeywords()).Fixes()
	for _, name := range m.msg.far.machines {
		if a := m.msg.far.answers[name]; a != nil {
			for _, x := range a.fixes {
				if !slices.Contains(fixes, x) {
					fixes = append(fixes, x)
				}
			}
		}
	}
	return fixes
}

// farNote says which machines the results do not cover in full: still searching, down, too old, building their store.
func (m *Model) farNote() string {
	var parts []string
	for _, name := range m.msg.far.machines {
		a := m.msg.far.answers[name]
		why := i18n.T("msg.far_searching")
		if a != nil && !a.asking {
			why = remote.Grepped{Err: a.err, Result: remote.GrepResult{Building: a.building}}.Gap()
		}
		if why != "" {
			parts = append(parts, i18n.F("remote.down", name, why))
		}
	}
	return strings.Join(parts, "  ·  ")
}

// inPane: hit x of r is in the transcript the right pane reads; another machine's, as far as its last read tells.
func (m *Model) inPane(r *tend.Rec, x found) bool {
	if r.Host == "" {
		return paths.SameFile(x.Path, transcript(r))
	}
	return x.file != "" && m.farSelf(r, x.file)
}

// farSelf: file is the transcript the right pane reads of r, or the pane has not read r yet.
func (m *Model) farSelf(r *tend.Rec, file string) bool {
	known := m.hosts.File(r)
	return known == "" || known == file
}

// openFarHits lists the hits of kw in another machine's session through its hits; a tend without hits leaves the right
// pane to step through what it has loaded.
func (m *Model) openFarHits(r *tend.Rec, kw string) tea.Cmd {
	if m.hosts == nil {
		m.jumpHit(0)
		return nil
	}
	var pin fulltext.Hit
	if x, ok := m.msgHit(r); ok && x.file != "" {
		pin = fulltext.Hit{Path: x.file, Off: x.Off}
	}
	key, h, name, ref := msgKey(r), m.hosts, r.Host, remote.Ref{Provider: r.Provider, SessionID: r.SessionID}
	m.closeHits()
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	m.msg.hl = hitList{rec: r, key: key, q: kw, loading: true, cancel: cancel}
	m.pane = paneList
	return func() tea.Msg {
		res, err := h.Hits(ctx, name, remote.HitsParams{Ref: ref, Q: kw, Limit: hitLimit})
		if ctx.Err() == context.Canceled {
			return nil
		}
		items := make([]fulltext.Hit, 0, len(res.Hits))
		for _, x := range res.Hits {
			items = append(items, fulltext.Hit{Path: x.File, Off: x.Off, Role: farRole(x.Role), At: x.At.Local(), Text: x.Text})
		}
		return hitsMsg{key: key, q: kw, pin: pin, items: items, total: res.Total, err: err}
	}
}

// farRole is a remote Hit's role as a text-store entry's.
func farRole(role string) byte {
	switch role {
	case "user":
		return 'u'
	case "tool":
		return 't'
	}
	return 'a'
}

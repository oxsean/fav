package tui

import (
	"cmp"
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/tend"
)

// Message search: a search box value starting with > (or 》) searches the prose of the sessions the rest of the query picks.
// The left list becomes the ranked sessions, the right pane opens on the first hit and n/N walk the hits.
// The full-text store follows the index: every index change starts an incremental update in the background.

const msgDebounce = 150 * time.Millisecond

type msgState struct {
	res     map[string]found // hits of the finished searches, by msgKey (records are rebuilt on a store reload)
	order   []string         // ranked, this machine's and the other machines' merged
	local   []msgItem        // this machine's ranked hits
	far     farSearch
	key     string // search the results belong to
	want    string // search asked for (debounced / running)
	seq     int
	busy    bool
	cancel  context.CancelFunc
	cands   []*tend.Rec // sessions the scope picks, set by refresh
	byTime  bool        // o: newest hit first instead of relevance
	shownQ  string      // query of the results on screen
	toTop   bool        // the next refresh puts the cursor on the first result
	candKey string      // identity of cands: a change reruns the search
	landQ   string      // keywords the right pane last landed for

	seek    *tend.Rec // paging back through this record until hit number seekHit is loaded
	seekHit int
	seekOff int64 // ≥0: paging back until the message at this offset is loaded, instead of seekHit
	hl      hitList
	hitQ    string // keywords the right pane last jumped for

	textRun   bool
	textAgain bool         // an index change arrived during the update: run once more
	textProg  atomic.Int64 // done<<32 | total of the running update
	textGen   int          // bumped when an update read something: the search reruns
	textBusy  bool         // the last update found another tend building the store
}

type msgTickMsg int
type msgResultMsg struct {
	seq  int
	key  string
	recs []*tend.Rec
	res  []fulltext.Result
}
type textDoneMsg struct {
	p   fulltext.Progress
	err error
}
type textTickMsg struct{}
type textRetryMsg struct{}

// msgQuery: the query after the > prefix, and whether the box is in message-search mode (not on the Agents page).
func (m *Model) msgQuery() (string, bool) {
	if m.view == viewLive {
		return "", false
	}
	return fulltext.Prefixed(m.search.Value())
}

func (m *Model) msgMode() bool { _, ok := m.msgQuery(); return ok }

// msgKeywords are the words searched for in message-search mode, "" otherwise.
func (m *Model) msgKeywords() string {
	q, ok := m.msgQuery()
	if !ok {
		return ""
	}
	kw, _ := fulltext.Split(q)
	return strings.ToLower(kw)
}

// findQuery is what the right pane highlights and n/N walks: its own \ query, else the message-search keywords.
func (m *Model) findQuery() string {
	if q := m.chat.query(); q != "" {
		return q
	}
	return m.msgKeywords()
}

// msgRows are the candidates that hold every keyword and the other machines' hits, in rank order.
func (m *Model) msgRows(recs []*tend.Rec) []row {
	in := make(map[string]*tend.Rec, len(recs))
	for _, r := range recs {
		in[msgKey(r)] = r
	}
	var out []row
	for _, k := range m.msg.order {
		if r := cmp.Or(in[k], m.msg.far.rows[k]); r != nil {
			out = append(out, row{rec: r})
		}
	}
	if m.msg.byTime {
		sort.SliceStable(out, func(i, j int) bool {
			a, _ := m.msgHit(out[i].rec)
			b, _ := m.msgHit(out[j].rec)
			return a.Latest.After(b.Latest)
		})
	}
	return out
}

func (m *Model) msgHit(r *tend.Rec) (found, bool) {
	x, ok := m.msg.res[msgKey(r)]
	return x, ok
}

// found is a session's hits: a remote one's transcript is named by file (fileio.ID there), not Path.
type found struct {
	fulltext.Result
	file string
}

type msgItem struct {
	key string
	rec *tend.Rec
	found
}

// msgKey: results key by machine and session, so records rebuilt on a store reload still match.
func msgKey(r *tend.Rec) string { return r.Host + "\x00" + r.Key() }

// mergeMsg ranks this machine's hits and every answered machine's as sessions.grep does.
func (m *Model) mergeMsg() {
	lists := [][]msgItem{m.msg.local}
	for _, name := range m.msg.far.machines {
		if a := m.msg.far.answers[name]; a != nil {
			lists = append(lists, a.items)
		}
	}
	items := remote.Interleave(lists, func(x msgItem) bool { return x.AllInOne }, 0)
	m.msg.res, m.msg.order = make(map[string]found, len(items)), make([]string, 0, len(items))
	for _, x := range items {
		m.msg.res[x.key] = x.found
		m.msg.order = append(m.msg.order, x.key)
	}
}

// issueMsgSearch schedules this machine's search when the query, scope or store changed, and the other machines'
// when the query or the machines shown changed; runs after every Update.
func (m *Model) issueMsgSearch() tea.Cmd {
	return tea.Batch(m.issueLocalSearch(), m.issueFarSearch())
}

func (m *Model) issueLocalSearch() tea.Cmd {
	key := ""
	if kw := m.msgKeywords(); kw != "" {
		q, _ := m.msgQuery()
		key = strings.Join([]string{strconv.Itoa(int(m.view)), q, strconv.Itoa(m.msg.textGen), m.msg.candKey}, "\x00")
	}
	if key == m.msg.want {
		return nil
	}
	m.msg.want = key
	if m.msg.cancel != nil {
		m.msg.cancel()
		m.msg.cancel = nil
	}
	m.msg.seq++
	if key == "" {
		m.msg.res, m.msg.order, m.msg.local, m.msg.key, m.msg.busy, m.msg.shownQ = nil, nil, nil, "", false, ""
		return nil
	}
	m.msg.busy = true
	seq := m.msg.seq
	return tea.Tick(msgDebounce, func(time.Time) tea.Msg { return msgTickMsg(seq) })
}

// runMsgSearch starts the debounced search; typing on cancels it through seq and the context.
func (m *Model) runMsgSearch(seq int) tea.Cmd {
	if seq != m.msg.seq {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.msg.cancel = cancel
	recs := append([]*tend.Rec(nil), m.msg.cands...)
	copies := make([]*tend.Rec, len(recs)) // ⚠️ the search runs off the main loop, which keeps editing the records
	for i, r := range recs {
		cp := *r
		copies[i] = &cp
	}
	bySession, kw, key, dir := m.idx.PathsBySession(), m.msgKeywords(), m.msg.want, fulltext.Dir()
	return func() tea.Msg {
		found := fulltext.Find(ctx, dir, copies, bySession, kw)
		if ctx.Err() != nil {
			return nil
		}
		return msgResultMsg{seq, key, recs, found.Results}
	}
}

func (m *Model) applyMsgResult(msg msgResultMsg) tea.Cmd {
	if msg.seq != m.msg.seq {
		return nil
	}
	m.msg.busy, m.msg.key, m.msg.cancel = false, msg.key, nil
	m.msg.local = make([]msgItem, 0, len(msg.res))
	for _, x := range msg.res {
		r := msg.recs[x.Cand]
		m.msg.local = append(m.msg.local, msgItem{msgKey(r), r, found{Result: x}})
	}
	return m.showMsgResults()
}

// showMsgResults lists the merged results; new keywords start at the top and land on the first result's hit.
func (m *Model) showMsgResults() tea.Cmd {
	m.mergeMsg()
	if q, _ := m.msgQuery(); q != m.msg.shownQ {
		m.msg.shownQ, m.msg.toTop = q, true
	}
	m.refresh()
	if m.msgKeywords() != m.msg.landQ && m.current() != nil { // new keywords: land on the new results' hit
		return m.landHit()
	}
	return nil
}

func candKey(recs []*tend.Rec) string {
	h := fnv.New64a()
	for _, r := range recs {
		h.Write([]byte(r.Key()))
		h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// syncText runs an incremental full-text update in the background; a second call while one runs queues one more.
func (m *Model) syncText(idx *index.Index) tea.Cmd {
	if m.msg.textRun {
		m.msg.textAgain = true
		return nil
	}
	m.msg.textRun, m.msg.textAgain = true, false
	indexed, prog, outLines := idx.Paths(), &m.msg.textProg, m.cfg.ToolOutput
	var pinned []*tend.Rec // ⚠️ copies: the update runs off the main loop, which keeps editing the records
	for _, r := range m.store.All() {
		if r.PinnedPath != "" {
			cp := *r
			pinned = append(pinned, &cp)
		}
	}
	update := func() tea.Msg {
		p, err := fulltext.Sync(context.Background(), indexed, pinned, outLines, func(p fulltext.Progress) {
			prog.Store(int64(p.Done)<<32 | int64(p.Total))
		})
		return textDoneMsg{p, err}
	}
	return tea.Batch(update, textTick())
}

func textTick() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return textTickMsg{} })
}

func (m *Model) applyTextDone(msg textDoneMsg) tea.Cmd {
	m.msg.textRun = false
	m.msg.textProg.Store(0)
	if errors.Is(msg.err, fulltext.ErrBusy) { // another tend is building the store: check back, rerun the search once it is done
		m.msg.textBusy = true
		return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return textRetryMsg{} })
	}
	if msg.p.Done > 0 || m.msg.textBusy {
		m.msg.textGen++
	}
	m.msg.textBusy = false
	if m.msg.textAgain {
		return m.syncText(m.idx)
	}
	return nil
}

// textProgress: done and total of the running update, zero when idle.
func (m *Model) textProgress() (int, int) {
	v := m.msg.textProg.Load()
	return int(v >> 32), int(v & 0xffffffff)
}

func (m *Model) msgTitle() string {
	title := ""
	switch {
	case m.msgKeywords() == "":
		title = i18n.T("msg.title_empty")
	case fulltext.TooLong(m.msgKeywords()):
		title = i18n.T("msg.too_long")
	case m.msg.busy && m.msg.key != m.msg.want:
		title = i18n.T("msg.title_searching")
	default:
		hits := 0
		for _, k := range m.msg.order {
			hits += m.msg.res[k].Hits
		}
		by := i18n.T("msg.sort_relevance")
		if m.msg.byTime {
			by = i18n.T("msg.sort_recent")
		}
		key := "msg.title"
		if m.typing {
			key = "msg.title_typing" // n/N would type into the box
		}
		also := ""
		if fixes := m.msgFixes(); len(fixes) > 0 {
			also = i18n.F("msg.also", strings.Join(fixes, " "))
		}
		if note := m.farNote(); note != "" {
			also += " · " + note
		}
		title = i18n.F(key, len(m.rows), hits, also, by)
	}
	if done, total := m.textProgress(); total >= 20 {
		title += i18n.F("msg.title_indexing", done, total)
	}
	return title
}

const seekMsgs = 200

// findHit shows hit k of the current record. Message search pages back only until hit k is loaded, so a huge
// transcript is not read whole on every cursor stop.
func (m *Model) findHit(k int) tea.Cmd {
	r := m.current()
	p := m.probes[r]
	if r == nil || p == nil || !p.done {
		return nil
	}
	if m.chat.query() != "" { // a \ search walks its hit list; this only steps through what is loaded
		m.jumpHit(k)
		return nil
	}
	if k < len(m.hits()) || p.full {
		m.msg.seek = nil
		m.jumpHit(k)
		return nil
	}
	m.msg.seek, m.msg.seekHit, m.msg.seekOff = r, k, -1
	return m.load(r, seekMsgs)
}

// landHit opens the right pane of the current record on the hit the list shows: the selected hit of an open hit list,
// else the result's best hit (the card's snippet), else the newest hit.
func (m *Model) landHit() tea.Cmd {
	r := m.current()
	if m.hitsOpen() {
		return m.selectHit(m.msg.hl.cur)
	}
	if r == nil || m.chat.query() != "" {
		return nil
	}
	if m.msg.key != m.msg.want {
		m.msg.landQ = "" // results are still coming; applyMsgResult lands
		return nil
	}
	m.msg.landQ = m.msgKeywords()
	if x, ok := m.msgHit(r); ok && x.Off >= 0 && m.inPane(r, x) {
		return m.showOff(x.Off)
	}
	return m.findHit(0)
}

// showOff puts the message at transcript offset off at the top of the right pane, paging back until it is loaded;
// when it is not there, the closest older message.
func (m *Model) showOff(off int64) tea.Cmd {
	r := m.current()
	p := m.probes[r]
	if r == nil {
		return nil
	}
	if p == nil || !p.done {
		m.msg.seek, m.msg.seekOff = r, off // the probe picks it up
		return nil
	}
	n := len(p.msgs)
	for i, msg := range p.msgs {
		if msg.Off == off {
			m.jumpMsg(i)
			return nil
		}
	}
	if p.full || n > 0 && p.msgs[n-1].Off < off {
		for i, msg := range p.msgs {
			if msg.Off < off {
				m.jumpMsg(i)
				break
			}
		}
		m.msg.seek = nil
		return nil
	}
	m.msg.seek, m.msg.seekOff = r, off
	return m.load(r, seekMsgs)
}

func (m *Model) jumpMsg(i int) {
	m.msg.seek = nil
	m.chatRec = m.current()
	m.chatScroll, m.chatSkip, m.chatCur, m.chatFollow = i, 0, i, true
	for k, h := range m.hits() {
		if h >= i {
			m.chat.cur = k
			break
		}
	}
	if hl := &m.msg.hl; hl.openWant && m.hitsOpen() {
		hl.openWant = false
		m.openHitMessage()
	}
}

// resumeSeek continues a seek once a page of r arrives.
func (m *Model) resumeSeek(r *tend.Rec) tea.Cmd {
	if m.msg.seek != r {
		return nil
	}
	if m.msg.seekOff >= 0 {
		return m.showOff(m.msg.seekOff)
	}
	return m.findHit(m.msg.seekHit)
}

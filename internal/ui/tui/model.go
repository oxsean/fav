// Package tui is the bubbletea front-end: layout, navigation, overlays; query parsing and previews come from internal/fav and internal/render.
package tui

import (
	"os"
	"slices"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/render"
)

const (
	wideCols    = 100 // two panes at and above
	compactCols = 60  // compact mode below: title and time only
	listRatio   = 42
	cardRows    = 5 // card: borders + title / source / tags
)

type view int

// tabs: favorites (store only, default) / sessions (whole index, by date) / projects (grouped) / Agents (running now)
const (
	viewFavorites view = iota
	viewSessions
	viewProjects
	viewLive
)

const viewCount = 4

// Result is what the TUI hands back on exit; resuming happens outside.
type Result struct {
	Resume  *fav.Rec
	NoHerdr bool
	Start   *capture.CommandSpec // a new session (fork, handoff) to run in this terminal
}

type row struct {
	group  string // non-empty = group header row, not selectable
	count  int
	rec    *fav.Rec
	folded bool
}

const (
	paneList = iota
	paneChat
)

type Model struct {
	store  *fav.Store
	cfg    fav.Config
	search textinput.Model
	typing bool

	view       view
	rows       []row
	cursor     int
	scroll     int // first visible screen line of the list, not the first record: cards span lines
	zones      []zone
	clickX     int
	pathOK     map[string]bool // stat cache, cleared on index change and storeTick
	chatInputX int
	lastHit    int
	lastTime   time.Time
	wheelAcc   int
	wheelStep  int
	wheelPend  int  // wheel events not yet applied: some terminals send one per pixel, a trackpad fling is thousands
	wheelX     int  // column of the last wheel event
	wheelTick  bool // a wheelTickMsg is scheduled
	reuseFrame bool // nothing changed: View returns the previous frame
	lastFrame  string
	sel        selection
	ovX, ovY   int // last frame's overlay box, limits drag selection
	ovW, ovH   int
	moved      bool            // arrow keys moved the selection while the search box had focus: Enter resumes instead of just leaving the box
	open       map[string]bool // expanded groups in the projects view; absent = collapsed
	startDir   string          // cwd at startup: its project group opens the first time the projects view shows it
	autoOpened bool
	detail     bool
	ov         overlay
	notice     string
	noticeSeq  int  // bumped by flash; an expiry only clears its own notice
	noticeNew  bool // flash ran during this Update: schedule the expiry
	w, h       int
	now        time.Time
	result     Result
	quitting   bool
	pending    tea.Cmd // async actions queued by click / key handlers, drained at the end of Update

	chipFocus int // focused chip; -1 = list
	sortBy    sortBy
	at        map[*fav.Rec]time.Time
	probes    map[*fav.Rec]*probe

	chatRec    *fav.Rec // which record the chat scroll belongs to; reset on change
	chatScroll int
	chatSkip   int // lines skipped inside the first message: the wheel scrolls by line, J/K by message
	chatCur    int
	chatFollow bool // highlight moved by keyboard → the viewport follows it; wheel → the highlight is clamped into view
	chatHit    int
	chatHitAt  time.Time
	pane       int
	chatW      int // last frame's chat width and rows, for wheel math and end detection
	chatRoom   int
	chatShown  int // messages shown last frame, to detect nearing the end
	chatY      int
	chatX      int
	chat       chatSearch
	hitsFor    *fav.Rec // hits() cache key: record, query, message count
	hitsQ      string
	hitsN      int
	hitList    []int
	probeWant  *fav.Rec // where the cursor last rested; probing waits probeDelay and is void if it moved
	probeSeq   int

	live      map[string]capture.Live
	liveHerdr map[string]capture.Live  // previous Herdr result, needed for "since when"
	pulse     map[string]capture.Pulse // running sessions' last reply / turn start / context, read after each live poll
	attn      map[string]attnEntry     // what the user has taken in of each running session (attention.json)
	lastNeed  map[string]int           // need() at the last check: a change to "needs you" is announced once
	lastWheel time.Time                // last wheel event; no index swap while scrolling
	heldIdx   *index.Index             // index that arrived mid-scroll, applied once scrolling stops
	idxGen    int                      // +1 after a project move: refreshes started before it return stale snapshots
	idx       *index.Index             // index snapshot, replaced whole by the background refresh
	nFav      int                      // tab totals, query-independent, recomputed when the index or the store changes
	nAll      int
	nProj     int
	unfav     []*fav.Rec          // unfavorited sessions from the index (empty ID); objects survive refreshes
	extra     *fav.Rec            // a session opened by id that the lists would not show (fav open)
	agents    map[string]*fav.Rec // status:agent rows, by session key
	msg       msgState            // > message search and the full-text store updates
	// a just-edited record stays in place until the cursor leaves, the filter or the tab changes
	pin     *fav.Rec
	pinKey  string
	synth   map[string]*fav.Rec   // live sessions the index has not seen, built from Herdr's title and cwd; objects survive refreshes
	groups  map[string][]*fav.Rec // records per project group incl. collapsed ones, for the right-pane project info
	projCur int                   // highlighted session in the project info; active when the right pane has focus on a group header
	trashed map[string]*fav.Rec   // trash cards; objects survive refreshes
	gone    map[string]bool       // sessions deleted by this process, hidden until the index drops them
}

func New(s *fav.Store, idx *index.Index, cfg fav.Config, initialQuery string) *Model {
	ti := textinput.New()
	ti.Placeholder = i18n.T("search.placeholder")
	ti.Prompt = render.GlyphSearch + "  "
	ti.SetValue(initialQuery)
	ti.CharLimit = 200

	m := &Model{
		store: s, idx: idx, cfg: cfg, search: ti, open: map[string]bool{}, attn: loadAttn(),
		w: 80, h: 24, now: time.Now(), chipFocus: -1, chat: newChatSearch(),
		view: view(indexOf(views, cfg.DefaultView)), sortBy: sortBy(indexOf(sorts, cfg.Sort)), wheelStep: max(1, cfg.WheelStep),
	}
	setWheelTuning(m.wheelStep, cfg.WheelSpeed)
	m.startDir, _ = os.Getwd()
	m.unfav = idx.Attach(s, nil)
	m.recount()
	m.refresh()
	return m
}

// Focus opens the sessions view on r (or the list's own object for that session) with the right pane active.
func (m *Model) Focus(r *fav.Rec) {
	if r == nil {
		return
	}
	if have := m.bySession(r.SessionID); have != nil {
		r = have
	} else {
		m.extra = r
	}
	m.view = viewSessions
	m.search.SetValue("")
	m.refresh()
	for i, row := range m.rows {
		if row.rec == r {
			m.cursor = i
			break
		}
	}
	m.pane = paneChat
}

func (m *Model) Init() tea.Cmd {
	openTrace()
	tracef("start %dx%d wheelStep=%d", m.w, m.h, m.wheelStep)
	if m.idx.Len() == 0 {
		m.flash(i18n.T("flash.building_index"))
	}
	if m.cfg.ResumeIn == fav.ResumeApp || m.cfg.ResumeIn == fav.ResumeOrigin {
		go func() { // the resume dialog's Enter depends on it; everyone else looks up on the first dialog
			capture.AppAvailable(fav.ProviderClaude)
			capture.AppAvailable(fav.ProviderCodex)
		}()
	}
	return tea.Batch(textinput.Blink, m.pollLive(), watchStore(), m.refreshIndex(), m.syncText(m.idx))
}

const indexEvery = 10 * time.Second

func (m *Model) refreshIndex() tea.Cmd {
	idx, gen := m.idx, m.idxGen
	return func() tea.Msg {
		next, changed := idx.Refresh()
		return indexMsg{next, changed, gen} // saved on the main loop after the seq check: a stale snapshot must not overwrite the forced rescan after a move
	}
}

type indexMsg struct {
	idx     *index.Index
	changed bool
	gen     int
}

type indexTickMsg struct{}

func (m *Model) applyIndex(idx *index.Index) {
	m.idx, m.pathOK = idx, nil
	m.unfav = idx.Attach(m.store, m.unfav)
	m.recount()
	m.refresh()
}

// watchStore stats records.jsonl every 2 s and reloads when another process changed it.
func watchStore() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return storeTickMsg{} })
}

type storeTickMsg struct{}

func (m *Model) syncStore() {
	if !m.store.Changed() {
		return
	}
	if err := m.store.Reload(); err != nil {
		m.flash(i18n.T("flash.reload_failed") + err.Error())
		return
	}
	m.probes, m.probeWant = nil, nil // new record objects, old probes no longer match
	m.pin, m.pinKey = nil, ""        // the pinned object is stale; writing it would overwrite another process's change
	m.unfav = m.idx.Attach(m.store, m.unfav)
	m.recount()
	m.refresh()
}

// probe is the part of the detail pane that hits disk or execs git (checks, recent chat), probed in the background once the cursor rests.
type probe struct {
	checks  []capture.Check
	msgs    []capture.Message // newest first: the last recentMsgs, then olderMsgs more per page
	from    int64
	done    bool
	full    bool
	loading bool
	fresh   []capture.Message // new messages of a live session, merged when the pane can refresh
	replace bool              // fresh does not line up with the existing messages (40+ new at once): replace everything
}

type probeMsg struct {
	rec    *fav.Rec
	checks []capture.Check
	page   capture.Page
}

const (
	recentMsgs   = 40                     // max messages the detail pane looks back; J/K pages
	chatBodyRows = 4                      // max body lines per message
	probeDelay   = 250 * time.Millisecond // how long the cursor rests before probing
)

type probeTickMsg int // carries probeSeq; stale ticks are dropped

func (m *Model) probeCurrent() tea.Cmd {
	r := m.current()
	if r == nil || m.probes[r] != nil {
		return nil
	}
	if m.probes == nil {
		m.probes = map[*fav.Rec]*probe{}
	}
	m.probes[r] = &probe{}
	return func() tea.Msg {
		var page capture.Page
		if path := transcript(r); path != "" {
			page = capture.Messages(path, -1, recentMsgs)
		}
		return probeMsg{r, capture.Checks(r), page}
	}
}

func (m *Model) isLive(sessionID string) bool { _, ok := m.live[sessionID]; return ok }

func (m *Model) Result() Result { return m.result }

func (m *Model) recount() {
	q := fav.Parse("status:all")
	m.nFav = len(m.store.Query(q))
	q.All = true
	all := m.store.Query(q)
	for _, r := range m.unfav {
		if q.Match(r) {
			all = append(all, r)
		}
	}
	projects := map[string]bool{}
	for _, r := range all {
		projects[r.Project] = true
	}
	m.nAll, m.nProj = len(all), len(projects)
}

func (m *Model) pinContext() string { return strconv.Itoa(int(m.view)) + "\x00" + m.search.Value() }

func (m *Model) query() fav.Query {
	s := m.search.Value()
	if rest, ok := m.msgQuery(); ok { // message search: the keywords are searched in the text, the rest picks the sessions
		_, s = fulltext.Split(rest)
	}
	q := fav.Parse(s)
	q.All, q.Live = m.view != viewFavorites, m.isLive
	if m.view == viewLive {
		q.Status, q.Turns = "live", 0
	}
	return q
}

func (m *Model) refresh() {
	q := m.query()
	recs := m.store.Query(q)
	if q.Status == fav.StatusTrash {
		recs = m.trashRecs(q)
	} else if q.Status == fav.StatusAgent {
		recs = m.agentRecs(q)
	} else if q.All {
		for _, r := range m.unfav {
			if q.Match(r) && !m.gone[r.Provider+":"+r.SessionID] {
				recs = append(recs, r)
			}
		}
		if m.extra != nil && !slices.Contains(recs, m.extra) {
			recs = append(recs, m.extra)
		}
	}
	cur := m.current()
	curGroup := ""
	if cur == nil && m.cursor < len(m.rows) {
		curGroup = m.rows[m.cursor].group
	}
	screenRow := m.rowTop(m.cursor) - m.scroll // the cursor's screen position, kept after a re-layout
	if m.pin != nil && (m.pin != cur || m.pinKey != m.pinContext()) {
		m.pin = nil
	}
	if m.pin != nil && !slices.Contains(recs, m.pin) {
		recs = append(recs, m.pin)
	}
	if m.msgMode() {
		m.msg.cands, m.msg.candKey = recs, candKey(recs)
		_, m.at = m.sortBy.sorted(recs)
		m.rows = m.msgRows(recs)
	} else if m.view == viewLive {
		recs = append(recs, m.synthLive(q)...)
		recs, m.at = m.sortBy.sorted(recs)
		m.rows = m.liveRows(recs)
	} else {
		recs, m.at = m.sortBy.sorted(recs)
		switch {
		case m.view == viewProjects:
			m.rows, m.groups = projectRows(recs, m.open, m.cfg.ProjectSort)
		case m.sortBy == sortTurns: // sorting by turns breaks date order: no groups
			m.rows = make([]row, 0, len(recs))
			for _, r := range recs {
				m.rows = append(m.rows, row{rec: r})
			}
		default:
			m.rows = timelineRows(recs, m.at, m.now)
		}
	}
	if m.msg.toTop { // a new message search: its results start at the top
		m.msg.toTop, m.cursor, m.scroll, cur, curGroup = false, 0, 0, nil, ""
	}
	// the cursor follows the record (or group), not the row index, and keeps its screen position so the list does not jump
	for i, r := range m.rows {
		if (cur != nil && r.rec == cur) || (cur == nil && curGroup != "" && r.group == curGroup) {
			m.cursor = i
			m.scroll = max(0, m.rowTop(i)-screenRow)
			break
		}
	}
	m.clampCursor()
	m.autoOpenProject()
}

// autoOpenProject: once, when the projects view first shows a group holding sessions of the start directory,
// expand it, put the cursor on its header and scroll the header to the top of the list.
func (m *Model) autoOpenProject() {
	if m.autoOpened || m.view != viewProjects || m.startDir == "" {
		return
	}
	best, n := "", 0
	for g, recs := range m.groups {
		c := 0
		for _, r := range recs {
			if paths.Nested(r.Cwd, m.startDir) {
				c++
			}
		}
		if c > n || c == n && c > 0 && g < best {
			best, n = g, c
		}
	}
	if best == "" {
		return
	}
	m.autoOpened = true
	m.open[best] = true
	m.refresh()
	for i, r := range m.rows {
		if r.group == best {
			total := 0
			for _, h := range m.rowHeights() {
				total += h
			}
			m.cursor, m.scroll = i, min(m.rowTop(i), max(0, total-m.listHeight()))
			return
		}
	}
}

func (m *Model) when(r *fav.Rec) time.Time {
	if t, ok := m.at[r]; ok {
		return t
	}
	return m.sortBy.at(r)
}

func timelineRows(recs []*fav.Rec, at map[*fav.Rec]time.Time, now time.Time) []row {
	var out []row
	last, head := "", -1
	for _, r := range recs {
		g := render.DayLabel(at[r], now)
		if g != last {
			out = append(out, row{group: g})
			last, head = g, len(out)-1
		}
		out[head].count++
		out = append(out, row{rec: r})
	}
	return out
}

// projectRows groups by project, groups ordered by their newest record; groups not in open are collapsed.
func projectRows(recs []*fav.Rec, open map[string]bool, order string) ([]row, map[string][]*fav.Rec) {
	byProject := map[string][]*fav.Rec{}
	var names []string
	for _, r := range recs {
		p := r.Project
		if p == "" {
			p = i18n.T("group.no_project")
		}
		if _, seen := byProject[p]; !seen {
			names = append(names, p)
		}
		byProject[p] = append(byProject[p], r)
	}

	var out []row
	for _, p := range orderGroups(names, byProject, order) {
		out = append(out, row{group: p, folded: !open[p], count: len(byProject[p])})
		if !open[p] {
			continue
		}
		for _, r := range byProject[p] {
			out = append(out, row{rec: r})
		}
	}
	return out, byProject
}

func (m *Model) current() *fav.Rec {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].rec
	}
	return nil
}

func (m *Model) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor, m.scroll = 0, 0
		return
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.rows[m.cursor].rec == nil && !m.selectable(m.cursor) {
		for _, delta := range []int{1, -1} {
			for i := m.cursor + delta; i >= 0 && i < len(m.rows); i += delta {
				if m.selectable(i) {
					m.cursor = i
					return
				}
			}
		}
	}
}

func (m *Model) selectable(i int) bool {
	return m.rows[i].rec != nil || (m.view == viewProjects && m.rows[i].group != "")
}

// move steps over selectable rows only; down from the chip row enters the list, up from the top stays.
func (m *Model) move(delta int) {
	if m.chipFocus >= 0 {
		if delta > 0 {
			m.chipFocus = -1
		}
		return
	}
	if len(m.rows) == 0 {
		return
	}
	i := m.cursor
	for step := 0; step < len(m.rows); step++ {
		i += delta
		if i < 0 || i >= len(m.rows) {
			return
		}
		if m.selectable(i) {
			m.cursor = i
			return
		}
	}
}

// foldAll: any open → collapse all, else expand all; fold non-nil forces it.
func (m *Model) foldAll(fold *bool) {
	recs := append(m.store.Query(m.query()), m.unfav...)
	anyOpen := false
	names := map[string]bool{}
	for _, r := range recs {
		p := r.Project
		if p == "" {
			p = i18n.T("group.no_project")
		}
		names[p] = true
		if m.open[p] {
			anyOpen = true
		}
	}
	want := !anyOpen
	if fold != nil {
		want = !*fold
	}
	for p := range names {
		m.open[p] = want
	}
	m.refresh()
}

func (m *Model) toggleGroup() {
	if m.view != viewProjects {
		return
	}
	g := m.groupUnderCursor()
	m.open[g] = !m.open[g]
	m.refresh()
}

func (m *Model) openGroup() {
	g := m.groupUnderCursor()
	if g != "" && !m.open[g] {
		m.open[g] = true
		m.refresh()
	}
}

func (m *Model) foldGroup() {
	g := m.groupUnderCursor()
	if g == "" {
		return
	}
	for i := m.cursor; i >= 0; i-- {
		if m.rows[i].group == g {
			m.cursor = i
			break
		}
	}
	if m.open[g] {
		m.open[g] = false
		m.refresh()
	}
}

func (m *Model) groupUnderCursor() string {
	for i := min(m.cursor, len(m.rows)-1); i >= 0; i-- {
		if m.rows[i].group != "" {
			return m.rows[i].group
		}
	}
	return ""
}

// chipRows: chips fall back to one plain-text line when the terminal is short.
func chipRows(h int) int {
	if h < 20 {
		return 1
	}
	return 3
}

// chromeHeight: top bar 1 + search box 3 + chips + footer 1; compact mode is search 1 + footer 1.
func chromeHeight(w, h int) int {
	if w < compactCols {
		return 2
	}
	return 5 + chipRows(h)
}

func (m *Model) listHeight() int {
	h := m.h - chromeHeight(m.w, m.h)
	if m.w < compactCols {
		return max(1, h)
	}
	if m.view == viewProjects {
		h -= 2
	} else {
		h--
	}
	return max(1, h)
}

// rowHeights: a timeline card is 5 box lines + 1 gap, everything else one line.
func (m *Model) rowHeights() []int {
	hs := make([]int, len(m.rows))
	card := m.view != viewProjects && m.w >= compactCols
	for i, r := range m.rows {
		switch {
		case r.group != "", !card:
			hs[i] = 1
		default:
			hs[i] = cardRows + 1
		}
	}
	return hs
}

func (m *Model) twoColumn() bool { return m.w >= wideCols && !m.detail }

func (m *Model) listWidth() int {
	if !m.twoColumn() {
		return m.w
	}
	return m.w * listRatio / 100
}

func (m *Model) rowTop(i int) int {
	top := 0
	for j, h := range m.rowHeights() {
		if j >= i {
			break
		}
		top += h
	}
	return top
}

func (m *Model) ensureVisible() {
	hs := m.rowHeights()
	if m.cursor < 0 || m.cursor >= len(hs) {
		m.scroll = 0
		return
	}
	top := 0
	for i := 0; i < m.cursor; i++ {
		top += hs[i]
	}
	bottom := top + hs[m.cursor]

	if m.scroll > top {
		m.scroll = top
	}
	if h := m.listHeight(); m.scroll+h < bottom {
		m.scroll = bottom - h
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *Model) setView(v view) {
	if m.view == v {
		return
	}
	m.view, m.cursor, m.scroll, m.rows = v, 0, 0, nil
	m.refresh()
	if n := len(m.chipData()); m.chipFocus >= n {
		m.chipFocus = n - 1
	}
}

func (m *Model) nextView() view { return (m.view + 1) % viewCount }

func (m *Model) toggleFavorite() {
	r := m.current()
	if r == nil {
		return
	}
	title := render.Truncate(r.Title, 30)
	fresh := r.ID == ""
	ok := m.edit(func(r *fav.Rec) {
		if r.Favorite() {
			r.FavoritedAt = nil
			return
		}
		now := time.Now()
		r.FavoritedAt = &now
	})
	switch {
	case !ok:
	case !r.Favorite():
		m.flash(i18n.F("flash.unfavorited", title))
	case fresh:
		m.flash(i18n.F("flash.favorited_fresh", title))
	default:
		m.flash(i18n.F("flash.favorited", title))
	}
}

// edit changes the current record and saves; an unsaved session gets a record first (not a favorite); a failed write is rolled back.
func (m *Model) edit(change func(*fav.Rec)) bool { return m.editRec(m.current(), change) }

// editRec changes r and saves; after a store reload it edits the new object with the same ID (another process wrote).
func (m *Model) editRec(r *fav.Rec, change func(*fav.Rec)) bool {
	if r == nil {
		return false
	}
	if r.ID != "" {
		if cur := m.store.Get(r.ID); cur != nil {
			r = cur
		}
	} else if cur := m.store.BySession(r.Provider, r.SessionID); cur != nil { // another process just favorited it
		m.dropUnfav(r)
		r = cur
	}
	fresh := r.ID == ""
	if fresh {
		r.ID = fav.NewID()
	}
	change(r)
	if err := m.store.Put(r); err != nil {
		if fresh {
			r.ID = ""
		}
		m.flash(i18n.T("flash.write_failed") + err.Error())
		return false
	}
	if fresh {
		m.dropUnfav(r)
	}
	m.pin, m.pinKey = r, m.pinContext()
	m.recount()
	m.refresh()
	return true
}

func (m *Model) dropUnfav(r *fav.Rec) {
	for i, x := range m.unfav {
		if x == r {
			m.unfav = append(m.unfav[:i], m.unfav[i+1:]...)
			return
		}
	}
}

func (m *Model) focusSearch() {
	m.typing, m.moved, m.pane = true, false, paneList
	m.search.Focus()
}

// focusMsgSearch opens the search box in message-search mode, keeping keywords already typed after >.
func (m *Model) focusMsgSearch() {
	if m.view == viewLive { // Agents has no message search
		m.setView(viewSessions)
	}
	if !m.msgMode() {
		m.search.SetValue("> ")
		m.search.CursorEnd()
		m.refresh()
	}
	m.focusSearch()
}

// clickRow: a click selects, a second click on the same row within a short time resumes.
func (m *Model) clickRow(i int) {
	if i < 0 || i >= len(m.rows) || m.rows[i].rec == nil {
		return
	}
	now := time.Now()
	if m.cursor == i && m.lastHit == i && now.Sub(m.lastTime) < 500*time.Millisecond {
		m.lastTime = time.Time{}
		m.askResume()
		return
	}
	m.cursor, m.lastHit, m.lastTime = i, i, now
	m.detail, m.pane = false, paneList
}

// flash shows s in the footer; Update schedules its expiry (noticeFor).
func (m *Model) flash(s string) { m.notice, m.noticeSeq, m.noticeNew = s, m.noticeSeq+1, true }

// noticeFor: how long a notice stays — 3 s, plus a second per 15 characters, at most 10 s.
func noticeFor(s string) time.Duration {
	return min(3*time.Second+time.Duration(utf8.RuneCountInString(s)/15)*time.Second, 10*time.Second)
}

type noticeExpiredMsg struct{ seq int }

type item struct {
	name  string
	label string
	count int // 0 hides the count
}

func tagsOf(recs []*fav.Rec) []item {
	counts := map[string]int{}
	for _, r := range recs {
		for _, t := range r.Tags {
			counts[t]++
		}
	}
	return byCount(counts)
}

func projectsOf(recs []*fav.Rec) []item {
	counts := map[string]int{}
	for _, r := range recs {
		if r.Project != "" {
			counts[r.Project]++
		}
	}
	return byCount(counts)
}

func providersOf(recs []*fav.Rec) []item {
	counts := map[string]int{}
	for _, r := range recs {
		if r.Provider != "" {
			counts[r.Provider]++
		}
	}
	return byCount(counts)
}

func byCount(counts map[string]int) []item {
	out := make([]item, 0, len(counts))
	for k, n := range counts {
		out = append(out, item{name: k, count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].name < out[j].name
	})
	return out
}

func (m *Model) chatVisible() bool { return m.twoColumn() || m.detail }

func (m *Model) atTop() bool {
	for i := 0; i < m.cursor && i < len(m.rows); i++ {
		if m.selectable(i) {
			return false
		}
	}
	return true
}

func (m *Model) moveChip(delta int) {
	n := len(m.chipData())
	m.chipFocus = (m.chipFocus + delta + n) % n
}

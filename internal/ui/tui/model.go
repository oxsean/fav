// Package tui is the bubbletea front-end: layout, navigation, overlays; query parsing and previews come from internal/tend and internal/render.
package tui

import (
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fulltext"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
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
	viewTasks
)

const viewCount = 5

// Result is what the TUI hands back on exit; resuming happens outside.
type Result struct {
	Resume  *tend.Rec
	NoHerdr bool
	Start   *agent.CommandSpec // a new session (fork, handoff) to run in this terminal
}

type row struct {
	group   string // non-empty = group header row: the group's key (a project's is projKey + its id)
	label   string // the header's text; the key when empty
	section string // a heading over the project groups or the automatic ones, not selectable
	count   int
	rec     *tend.Rec
	folded  bool
}

const (
	paneList = iota
	paneChat
)

type Model struct {
	store  *tend.Store
	cfg    tend.Config
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
	rowClicks  clicks
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
	undo       *undoable
	w, h       int
	now        time.Time
	result     Result
	quitting   bool
	pending    tea.Cmd // async actions queued by click / key handlers, drained at the end of Update

	chipFocus int // focused chip; -1 = list
	sortBy    sortBy
	at        map[*tend.Rec]time.Time
	probes    map[*tend.Rec]*probe

	chatRec    *tend.Rec // which record the chat scroll belongs to; reset on change
	chatScroll int
	chatSkip   int // lines skipped inside the first message: the wheel scrolls by line, J/K by message
	chatCur    int
	chatFollow bool // highlight moved by keyboard → the viewport follows it; wheel → the highlight is clamped into view
	chatClicks clicks
	pane       int
	chatW      int // last frame's chat width and rows, for wheel math and end detection
	chatRoom   int
	chatShown  int // messages shown last frame, to detect nearing the end
	chat       chatSearch
	hitsFor    *tend.Rec // hits() cache key: record, query, message count
	hitsQ      string
	hitsN      int
	hitList    []int
	probeWant  *tend.Rec // where the cursor last rested; probing waits probeDelay and is void if it moved
	probeSeq   int

	live       map[string]capture.Live
	liveHerdr  map[string]capture.Live  // previous Herdr result, needed for "since when"
	follow     follow                   // running sessions' transcripts stat-ed every second (follow.go)
	pulse      map[string]capture.Pulse // running sessions' last reply / turn start / context, read after each live poll
	attn       map[string]attnEntry     // what the user has taken in of each running session (attention.json)
	lastNeed   map[string]int           // need() at the last check: a change to "needs you" is announced once
	lastWheel  time.Time                // last wheel event; no index swap while scrolling
	mouse      bool                     // mouse reporting on; View asks the terminal for it
	themed     bool                     // the terminal's background is known (or will not be): View draws only from then on
	heldIdx    *index.Index             // index that arrived mid-scroll, applied once scrolling stops
	forced     map[string]bool          // files every background rescan reads from scratch until one is applied
	idxGen     int                      // +1 after a project move: refreshes started before it return stale snapshots
	idx        *index.Index             // index snapshot, replaced whole by the background refresh
	nFavorites int                      // tab totals, query-independent, recomputed when the index or the store changes
	nAll       int
	nProj      int
	unfav      []*tend.Rec // unfavorited sessions from the index (empty ID); objects survive refreshes
	extra      *tend.Rec   // a session opened by id that the lists would not show (tend open)
	lists      index.Rows  // the rows it makes up (trash, agent runs, unindexed live sessions) survive refreshes
	msg        msgState    // > message search and the full-text store updates
	// a just-edited record stays in place until the cursor leaves, the filter or the tab changes
	pin     *tend.Rec
	pinKey  string
	groups  map[string][]*tend.Rec // records per project group incl. collapsed ones, for the right-pane project info
	projCur int                    // highlighted session in the project info; active when the right pane has focus on a group header

	hosts  *remote.Hosts        // other machines; nil = none configured, nothing is contacted
	here   func() remote.Peer   // this machine as a handoff reaches it; nil: remote.Here
	remote map[string]*hostRows // by host name
	far    servedHosts          // mode 2: how the other machines are read through the server
	people people               // users' names, as the coordinator gives them

	tasks tasksState
	proj  projectsState
}

func New(s *tend.Store, idx *index.Index, cfg tend.Config, initialQuery string) *Model {
	ti := newInput()
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
	useSkin(cfg)
	m.startDir, _ = os.Getwd()
	m.initProjects()
	m.unfav = idx.Attach(s, nil)
	m.recount()
	m.refresh()
	return m
}

// Focus opens the sessions view on r (or the list's own object for that session) with the right pane active.
func (m *Model) Focus(r *tend.Rec) {
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
	if m.cfg.ResumeIn == tend.ResumeApp || m.cfg.ResumeIn == tend.ResumeOrigin {
		go func() { // the resume dialog's Enter depends on it; everyone else looks up on the first dialog
			capture.AppAvailable(tend.ProviderClaude)
			capture.AppAvailable(tend.ProviderCodex)
		}()
	}
	var dial tea.Cmd
	if m.served() { // mode 2 dials at start: the lists need the server's projects, the Tasks view uses the same connection
		dial = m.tasksOpen()
	}
	return tea.Batch(textinput.Blink, askTheme(), m.pollLive(), watchStore(), m.refreshIndex(), m.syncText(m.idx), m.fetchHosts(), dial)
}

const indexEvery = 10 * time.Second

func (m *Model) refreshIndex() tea.Cmd {
	idx, gen, force := m.idx, m.idxGen, maps.Clone(m.forced)
	return func() tea.Msg {
		next, changed := idx.Rescan(force)
		return indexMsg{next, changed, gen, false} // saved on the main loop after the seq check: a stale snapshot must not overwrite the forced rescan after a move
	}
}

type indexMsg struct {
	idx     *index.Index
	changed bool
	gen     int
	once    bool // from reindex: the periodic refresh keeps its own schedule
}

// reindex rescans in the background (force: files to read from scratch); refreshes already running come back stale.
func (m *Model) reindex(force map[string]bool) tea.Cmd {
	m.heldIdx, m.idxGen = nil, m.idxGen+1
	if m.forced == nil {
		m.forced = map[string]bool{}
	}
	maps.Copy(m.forced, force)
	idx, gen, force := m.idx, m.idxGen, maps.Clone(m.forced)
	return func() tea.Msg {
		next, _ := idx.Rescan(force)
		return indexMsg{next, true, gen, true}
	}
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
		m.flash(i18n.F("flash.reload_failed", err))
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
	polling bool              // a re-read of a live session's tail is in flight
	fresh   []capture.Message // new messages of a live session, merged when the pane can refresh
	replace bool              // fresh does not line up with the existing messages (40+ new at once): replace everything
	failed  bool              // a read failed: no more pages until the host answers again and it is probed anew
}

type probeMsg struct {
	rec    *tend.Rec
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
		m.probes = map[*tend.Rec]*probe{}
	}
	m.probes[r] = &probe{}
	src := m.hosts.Source(r) // ⚠️ rows are reused and refreshed in place: the source reads a copy
	return func() tea.Msg {
		page := src.Messages(-1, recentMsgs)
		return probeMsg{r, src.Checks(), page}
	}
}

// reprobe reads r again from the end: its transcript was rewritten, the offsets held no longer line up.
func (m *Model) reprobe(r *tend.Rec) tea.Cmd {
	delete(m.probes, r)
	m.chatScroll, m.chatSkip = 0, 0
	return m.probeCurrent()
}

func (m *Model) isLive(sessionID string) bool { _, ok := m.live[sessionID]; return ok }

func (m *Model) Result() Result { return m.result }

func (m *Model) recount() {
	q := tend.Parse("status:all")
	m.nFavorites = len(m.store.Query(q))
	q.All = true
	all := m.list(q)
	projects := map[string]bool{}
	for _, r := range all {
		projects[groupKey(r)] = true
	}
	m.nAll, m.nProj = len(all), len(projects)
}

func (m *Model) pinContext() string { return strconv.Itoa(int(m.view)) + "\x00" + m.search.Value() }

func (m *Model) query() tend.Query {
	s := m.search.Value()
	if rest, ok := m.msgQuery(); ok { // message search: the keywords are searched in the text, the rest picks the sessions
		_, s = fulltext.Split(rest)
	}
	q := tend.Parse(s)
	q.All, q.Live, q.Also = m.view != viewFavorites, m.isLive, m.taskText
	if m.view == viewLive {
		q.Status, q.Turns = tend.StatusLive, 0
	}
	return q
}

func (m *Model) list(q tend.Query) []*tend.Rec {
	recs, err := m.lists.List(m.store, m.idx, m.unfav, m.live, q)
	if err != nil {
		m.flash(i18n.F("flash.trash_read_failed", err))
	}
	return append(recs, m.remoteList(q)...)
}

func (m *Model) refresh() {
	if m.view == viewTasks {
		m.rows = nil
		m.filterTasks()
		return
	}
	q := m.query()
	recs := m.list(q)
	if m.extra != nil && q.All && q.Status != tend.StatusTrash && q.Status != tend.StatusAgent && !slices.Contains(recs, m.extra) {
		recs = append(recs, m.extra)
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

func (m *Model) when(r *tend.Rec) time.Time {
	if t, ok := m.at[r]; ok {
		return t
	}
	return m.sortBy.at(r)
}

func timelineRows(recs []*tend.Rec, at map[*tend.Rec]time.Time, now time.Time) []row {
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

// projKey starts a project group's key, before the project's id: no directory name, an automatic group's key, does.
const projKey = "\x00"

// groupKey is the projects view's group of r: its project, else its automatic group (the directory's name).
func groupKey(r *tend.Rec) string {
	switch {
	case r.ProjectID != "":
		return projKey + r.ProjectID
	case r.Project == "":
		return i18n.T("group.no_project")
	}
	return r.Project
}

// groupProject is the project id of group key g, "" for an automatic group.
func groupProject(g string) string {
	id, _ := strings.CutPrefix(g, projKey)
	if id == g {
		return ""
	}
	return id
}

// projectRows groups by project: the projects' groups first, then the automatic ones, each part under a heading when
// there are projects and ordered on its own (by the newest record, the count or the name); groups not in open are
// collapsed.
func projectRows(recs []*tend.Rec, open map[string]bool, order string) ([]row, map[string][]*tend.Rec) {
	byGroup := map[string][]*tend.Rec{}
	labels := map[string]string{}
	var inProject, auto []string
	for _, r := range recs {
		g := groupKey(r)
		if _, seen := byGroup[g]; !seen {
			labels[g] = r.Group()
			if r.ProjectID != "" {
				inProject = append(inProject, g)
			} else {
				labels[g] = g
				auto = append(auto, g)
			}
		}
		byGroup[g] = append(byGroup[g], r)
	}

	var out []row
	add := func(keys []string) {
		for _, g := range orderGroups(keys, byGroup, labels, order) {
			out = append(out, row{group: g, label: labels[g], folded: !open[g], count: len(byGroup[g])})
			if !open[g] {
				continue
			}
			for _, r := range byGroup[g] {
				out = append(out, row{rec: r})
			}
		}
	}
	if len(inProject) > 0 {
		out = append(out, row{section: i18n.F("group.projects", len(inProject))})
		add(inProject)
		if len(auto) > 0 {
			out = append(out, row{section: i18n.F("group.unfiled", len(auto))})
		}
	}
	add(auto)
	return out, byGroup
}

func (m *Model) current() *tend.Rec {
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
	anyOpen := false
	for g := range m.groups {
		anyOpen = anyOpen || m.open[g]
	}
	want := !anyOpen
	if fold != nil {
		want = !*fold
	}
	for g := range m.groups {
		m.open[g] = want
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
	if v == viewTasks {
		m.pending = tea.Batch(m.pending, m.tasksOpen())
	}
	if n := len(m.chipData()); m.chipFocus >= n {
		m.chipFocus = n - 1
	}
}

func (m *Model) nextView() view { return (m.view + 1) % viewCount }

func (m *Model) toggleFavorite(r *tend.Rec) {
	if r == nil {
		return
	}
	title, fresh, p := render.Truncate(r.Title, 30), r.ID == "", r.ToggleFavorite()
	back := p.Undo(r)
	m.editRec(r, p, nil, func(m *Model, r *tend.Rec) {
		undo := m.restoreRec(r, back)
		switch {
		case !r.Favorite():
			m.offerUndo(i18n.F("flash.unfavorited", title), undo)
		case fresh:
			m.flash(i18n.F("flash.favorited_fresh", title))
			m.undo = &undoable{at: time.Now(), seq: m.noticeSeq, back: undo}
		default:
			m.offerUndo(i18n.F("flash.favorited", title), undo)
		}
	})
}

func (m *Model) toggleArchive(r *tend.Rec) {
	if r == nil {
		return
	}
	p := r.ToggleArchived()
	back := p.Undo(r)
	m.editRec(r, p, nil, func(m *Model, r *tend.Rec) {
		undo := m.restoreRec(r, back)
		if r.Archived() {
			m.offerUndo(i18n.F("flash.archived", r.Title), undo)
		} else {
			m.offerUndo(i18n.F("flash.unarchived", r.Title), undo)
		}
	})
}

func (m *Model) toggleStatus(r *tend.Rec, target string) {
	if r == nil {
		return
	}
	p := r.ToggleStatus(target)
	back := p.Undo(r)
	m.editRec(r, p, nil, func(m *Model, r *tend.Rec) {
		undo := m.restoreRec(r, back)
		if r.Status == target {
			m.offerUndo(i18n.F("flash.status_set", render.StatusLabel(target), r.Title), undo)
		} else {
			m.offerUndo(i18n.F("flash.back_to_doing", r.Title), undo)
		}
	})
}

// editRec writes p to r, then done runs with the record written; nothing runs when the write fails. This machine's
// record goes through Store.Update at once (an unsaved session gets a record, not a favorite); another machine's goes
// through that machine's put in the background (putRec), and done runs when it answers. expect, for another
// machine's: the record's updated_at the editor saw.
func (m *Model) editRec(r *tend.Rec, p tend.Patch, expect *time.Time, done func(*Model, *tend.Rec)) {
	if r == nil {
		return
	}
	if r.Host != "" {
		m.putRec(r, p, expect, done)
		return
	}
	m.syncStore()
	unsaved := r.ID == ""
	now := time.Now()
	saved, err := m.store.Update(r, func(r *tend.Rec) { p.Apply(r, now) })
	if err != nil {
		m.flash(i18n.F("flash.write_failed", err))
		return
	}
	if unsaved {
		m.dropUnfav(r)
	}
	m.pin, m.pinKey = saved, m.pinContext()
	m.recount()
	m.refresh()
	if done != nil {
		done(m, saved)
	}
}

func (m *Model) dropUnfav(r *tend.Rec) {
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
	if m.rowClicks.double(i) && m.cursor == i {
		m.askResume()
		return
	}
	m.cursor = i
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

func tagsOf(recs []*tend.Rec) []item {
	return countBy(recs, func(r *tend.Rec) []string { return r.Tags })
}

// projectsOf: the projects of recs first, each named by its id and labelled with its name, then the automatic groups.
func projectsOf(recs []*tend.Rec) []item {
	inProject := countBy(recs, func(r *tend.Rec) []string { return []string{r.ProjectID} })
	names := map[string]string{}
	for _, r := range recs {
		names[r.ProjectID] = r.ProjectName
	}
	for i := range inProject {
		inProject[i].label = render.GlyphProject + " " + names[inProject[i].name]
	}
	return append(inProject, countBy(recs, func(r *tend.Rec) []string {
		if r.ProjectID != "" {
			return nil
		}
		return []string{r.Project}
	})...)
}
func providersOf(recs []*tend.Rec) []item {
	return countBy(recs, func(r *tend.Rec) []string { return []string{r.Provider} })
}

func countBy(recs []*tend.Rec, keys func(*tend.Rec) []string) []item {
	counts := tend.CountBy(recs, keys)
	out := make([]item, len(counts))
	for i, c := range counts {
		out[i] = item{name: c.Name, count: c.N}
	}
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

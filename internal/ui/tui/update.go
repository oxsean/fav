package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.search.Width = max(10, m.w-6)

	case tea.MouseMsg:
		cmd = m.handleMouse(msg)

	case wheelTickMsg:
		m.wheelTick = false
		m.applyWheel()

	case msgTickMsg:
		cmd = m.runMsgSearch(int(msg))

	case msgResultMsg:
		cmd = m.applyMsgResult(msg)

	case textDoneMsg:
		cmd = m.applyTextDone(msg)

	case textTickMsg:
		if m.msg.textRun {
			cmd = textTick() // redraws the progress in the title
		}

	case tea.KeyMsg:
		if isMouseFragment(msg) {
			tracef("fragment %q", msg.String())
			break
		}
		m.traceKey(msg)
		if msg.String() == "ctrl+c" {
			m.quitting = true
			break
		}
		m.notice = ""
		switch {
		case m.ov.active():
			cmd = m.overlayKey(msg)
		case m.chat.typing:
			cmd = m.chatSearchKey(msg)
		case m.typing:
			cmd = m.searchKey(msg)
		default:
			cmd = m.navKey(msg)
		}

	case herdrDoneMsg:
		if msg.err != nil {
			m.flash(i18n.F("flash.herdr_failed", msg.err.Error()))
			break
		}
		if !msg.focused {
			if err := capture.MarkResumed(m.store, msg.rec); err != nil {
				m.flash(i18n.T("flash.count_not_saved") + err.Error())
			}
		}
		m.refresh()
		note := msg.msg
		if msg.warn != nil {
			note += "；" + msg.warn.Error()
		}
		m.flash(note)

	case liveMsg:
		m.applyLive(msg)
		cmd = tea.Tick(liveEvery, func(time.Time) tea.Msg { return liveTickMsg{} })

	case liveTickMsg:
		cmd = tea.Batch(m.pollLive(), m.refreshChat())

	case refreshMsg:
		m.stash(msg)
		if p := m.probes[msg.rec]; p != nil && len(p.fresh) > 0 {
			tracef("refresh %s +%d replace=%v", msg.rec.SessionID, len(p.fresh), p.replace)
		}

	case storeTickMsg:
		m.pathOK = nil // cwd / transcript existence is re-checked every 2 s, not on every key
		m.syncStore()
		cmd = watchStore()

	case indexMsg:
		if msg.changed && msg.gen == m.idxGen {
			msg.idx.Save() // a failed write only slows the next start
			m.heldIdx = msg.idx
			if strings.HasPrefix(m.notice, i18n.T("flash.building_index_prefix")) {
				m.notice = ""
			}
		}
		cmd = tea.Tick(indexEvery, func(time.Time) tea.Msg { return indexTickMsg{} })
		if msg.changed && msg.gen == m.idxGen {
			cmd = tea.Batch(cmd, m.syncText(msg.idx))
		}

	case indexTickMsg:
		cmd = m.refreshIndex()

	case probeMsg:
		tracef("probe %s msgs=%d cur=%v", msg.rec.SessionID, len(msg.page.Msgs), msg.rec == m.current())
		if p := m.probes[msg.rec]; p != nil {
			p.checks, p.msgs, p.from, p.full, p.done = msg.checks, msg.page.Msgs, msg.page.From, msg.page.Done, true
			if m.findQuery() != "" && msg.rec == m.current() {
				cmd = tea.Batch(cmd, m.landHit())
			}
		}

	case pageMsg:
		tracef("page %s +%d done=%v cur=%v %s", msg.rec.SessionID, len(msg.page.Msgs), msg.page.Done, msg.rec == m.current(), m.traceChat())
		m.applyPage(msg)
		if msg.rec == m.current() && m.findQuery() != "" {
			switch {
			case m.msg.seek == msg.rec:
				cmd = tea.Batch(cmd, m.resumeSeek(msg.rec))
			}
		}

	case textRetryMsg:
		cmd = m.syncText(m.idx)

	case hitsMsg:
		cmd = m.applyHits(msg)

	case probeTickMsg:
		if int(msg) == m.probeSeq {
			cmd = tea.Batch(cmd, m.probeCurrent())
		}
	}
	if m.quitting {
		return m, tea.Quit
	}
	// no index swap while scrolling; wait a second after it stops
	if m.heldIdx != nil && time.Since(m.lastWheel) > time.Second {
		tracef("index apply")
		m.applyIndex(m.heldIdx)
		m.heldIdx = nil
	}
	m.applyFresh()
	// nearing the loaded end (under 10 left or the pane not full): fetch an older page
	if r := m.current(); r != nil {
		if p := m.probes[r]; p != nil && p.done && !p.full && !p.loading &&
			(m.chatScroll+m.chatShown >= len(p.msgs)-10 || !m.chatFills(m.chatScroll, m.chatSkip)) {
			tracef("older %s %s", r.SessionID, m.traceChat())
			cmd = tea.Batch(cmd, m.ensureOlder(r))
		}
	}
	if m.pending != nil {
		cmd, m.pending = tea.Batch(cmd, m.pending), nil
	}
	cmd = tea.Batch(cmd, m.issueMsgSearch())
	if m.msg.hl.key != "" && !m.hitsOpen() {
		m.dropHits()
	}
	if m.pane == paneChat && m.hitsOpen() {
		m.followChat()
	}
	if kw := m.msgKeywords(); kw != m.msg.hitQ {
		m.msg.hitQ, m.msg.seek = kw, nil
		if r := m.current(); r == m.probeWant && m.findQuery() != "" {
			cmd = tea.Batch(cmd, m.landHit())
		}
	}
	if r := m.current(); r != m.probeWant {
		m.msg.seek = nil
		if m.findQuery() != "" {
			cmd = tea.Batch(cmd, m.landHit())
		}
		m.probeWant = r
		m.probeSeq++
		seq := m.probeSeq
		cmd = tea.Batch(cmd, tea.Tick(probeDelay, func(time.Time) tea.Msg { return probeTickMsg(seq) }))
	}
	return m, cmd
}

// herdrDoneMsg is the receipt of a Herdr resume; the TUI stays open.
type herdrDoneMsg struct {
	rec     *fav.Rec
	msg     string
	focused bool // focusing an already-running tab is not a resume
	warn    error
	err     error
}

func (m *Model) finish(res Result) {
	m.result, m.quitting = res, true
}

// searchKey: letters type into the search box; arrows and paging move the list, Enter after a selection resumes.
func (m *Model) searchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.typing = false
		m.search.Blur()
		m.refresh()
		return nil
	case tea.KeyEnter:
		m.typing = false
		m.search.Blur()
		m.refresh()
		if m.msgMode() { // message search: Enter only ends typing, so n/N and → reach the hits
			m.pane = paneList
			return nil
		}
		if m.chipFocus < 0 && m.current() != nil && m.moved {
			return m.navKey(msg)
		}
		return nil
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyCtrlN, tea.KeyCtrlP:
		m.moved = true
		return m.navKey(msg)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.refresh()
	return cmd
}

// navKey: list navigation. ⚠️ Every action needs a non-letter key (letters never reach here under a CJK IME); 、？；， count as / ? ; , (a CJK input method types / as 、); ctrl+s stands in for \.
func (m *Model) navKey(msg tea.KeyMsg) tea.Cmd {
	if m.inTrash() && m.current() != nil && m.chipFocus < 0 && m.pane != paneChat {
		switch msg.String() {
		case "f", "x", "a", "e", "r", "X", "M", " ", "ctrl+g":
			m.flash(i18n.T("trash.in_trash_hint"))
			return nil
		}
	}
	if m.hitsOpen() && m.pane == paneList && m.chipFocus < 0 {
		if cmd, ok := m.hitKey(msg.String()); ok {
			return cmd
		}
	}
	switch msg.String() {
	case "\\", "ctrl+s":
		return m.startChatSearch()
	case "n":
		if m.hitsOpen() {
			return m.selectHit(m.msg.hl.cur + 1)
		}
		return m.findHit(m.chat.cur + 1)
	case "N":
		if m.hitsOpen() {
			return m.selectHit(m.msg.hl.cur - 1)
		}
		m.jumpHit(m.chat.cur - 1)
	case "q", "ctrl+c":
		m.quitting = true
	case "esc":
		// Esc backs out one level, never quits
		switch {
		case m.chipFocus >= 0:
			m.chipFocus = -1
		case m.chat.query() != "":
			m.chat.input.SetValue("")
		case m.pane == paneChat:
			m.pane = paneList
		case m.detail:
			m.detail = false
		case m.search.Value() != "":
			m.search.SetValue("")
			m.refresh()
		}
	case "/", "、": // a CJK input method turns / into 、
		m.focusSearch()
	case ">", "》":
		m.focusMsgSearch()
	case "j", "down", "ctrl+n":
		switch {
		case m.projectFocus():
			m.projCur++
		case m.pane == paneChat:
			m.moveChat(1)
		default:
			m.move(1)
		}
	case "k", "up", "ctrl+p":
		switch {
		case m.projectFocus():
			m.projCur--
		case m.pane == paneChat:
			m.moveChat(-1)
		default:
			m.move(-1)
		}
	case "J", "ctrl+j":
		m.moveChat(1)
	case "K", "ctrl+k":
		m.moveChat(-1)
	case "h", "left": // ← backs out: chip row → right pane → narrow detail → collapse group
		switch {
		case m.chipFocus >= 0:
			m.moveChip(-1)
		case m.pane == paneChat:
			m.pane = paneList
		case m.detail:
			m.detail = false
		case m.view == viewProjects: // tree convention: ← collapses; on a child it goes to the header
			m.foldGroup()
		}
	case "l", "right": // → goes in: expand group → right pane → full message
		switch {
		case m.chipFocus >= 0:
			m.moveChip(1)
		case m.pane == paneList && m.msgMode() && m.current() != nil:
			return m.openHits(m.msgKeywords())
		case m.view == viewProjects && m.current() == nil && !m.open[m.groupUnderCursor()]:
			m.openGroup()
		case m.pane == paneChat && m.current() != nil:
			return m.openMessage()
		case m.chatVisible(): // → again on a group header focuses the right-pane session list
			m.pane, m.projCur = paneChat, 0
		}
	case "y", "ctrl+y":
		if m.pane == paneChat {
			m.copyMessage()
		}
	case "g", "home":
		if m.pane == paneChat {
			m.moveChat(-1 << 30)
		} else {
			m.cursor = 0
			m.clampCursor()
		}
	case "G", "end":
		if m.pane == paneChat {
			m.moveChat(1 << 30)
		} else {
			m.cursor = len(m.rows) - 1
			m.clampCursor()
		}
	case "pgdown", "ctrl+f":
		if m.pane == paneChat {
			m.moveChat(5)
		} else {
			m.page(1)
		}
	case "pgup", "ctrl+b":
		if m.pane == paneChat {
			m.moveChat(-5)
		} else {
			m.page(-1)
		}
	case "ctrl+d":
		if m.pane == paneChat {
			m.moveChat(3)
		} else {
			m.halfPage(1)
		}
	case "ctrl+u":
		if m.pane == paneChat {
			m.moveChat(-3)
		} else {
			m.halfPage(-1)
		}
	case "tab":
		// switching views keeps the query
		m.setView(m.nextView())
	case "shift+tab":
		m.setView(view((int(m.view) + viewCount - 1) % viewCount))
	case "1", "2", "3", "4":
		m.setView(view(int(msg.String()[0] - '1')))
	case ";", "；":
		if m.chipFocus >= 0 {
			m.chipFocus = -1
		} else {
			m.chipFocus = 0
		}
	case "f", "*": // *: non-letter, IME-safe
		m.toggleFavorite()
	case " ", "ctrl+g":
		if m.current() == nil {
			m.toggleGroup()
		} else if m.twoColumn() || m.detail {
			m.askResume()
			if d, t := m.broken(m.current()); !d && !t {
				m.doResume(false)
			}
		}
	case "z":
		if m.view == viewProjects {
			m.foldAll(nil)
		}
	case "-", "=", "+":
		if m.view == viewProjects {
			fold := msg.String() == "-"
			m.foldAll(&fold)
		}
	case "enter":
		if m.chipFocus >= 0 {
			m.chipData()[m.chipFocus].open(m)
			return nil
		}
		if m.projectFocus() {
			m.jumpProject()
			return nil
		}
		if m.pane == paneChat {
			return m.openMessage()
		}
		if m.current() == nil {
			m.toggleGroup()
			return nil
		}
		// narrow screens open the detail first, wide ones the resume dialog
		if !m.twoColumn() && !m.detail {
			if m.current() != nil {
				m.detail = true
			}
			return nil
		}
		m.askResume()
	case "X":
		m.closeLive()
	case "r":
		m.askResume()
	case "t":
		m.pickTags()
	case "p":
		m.pickProjects()
	case "v":
		m.cycleProvider()
	case "s":
		m.pickStatus()
	case "D":
		m.askDelete()
	case "M":
		m.askMove()
	case "d":
		m.pickDate()
	case "o", "ctrl+o":
		switch {
		case m.msgMode():
			m.msg.byTime, m.msg.toTop = !m.msg.byTime, true
		case m.view == viewLive:
			m.cfg.LiveSort = liveSorts[(indexOf(liveSorts, m.cfg.LiveSort)+1)%len(liveSorts)]
			m.saveConfig()
		case m.view == viewProjects:
			m.cfg.ProjectSort = projSorts[(indexOf(projSorts, m.cfg.ProjectSort)+1)%len(projSorts)]
			m.saveConfig()
		default:
			m.sortBy = m.sortBy.next()
		}
		m.refresh()
	case "a", "ctrl+a":
		m.toggleArchive()
	case "x", "ctrl+x":
		m.toggleStatus(fav.StatusDone)
	case "e", "ctrl+e":
		return m.openEdit()
	case "?", "？":
		m.ov = overlay{kind: ovHelp}
	case ",", "，":
		m.openSettings()
	}
	return nil
}

func (m *Model) page(dir int) {
	step := max(1, m.listHeight()/max(1, cardRows+1))
	for i := 0; i < step; i++ {
		m.move(dir)
	}
}

func (m *Model) halfPage(dir int) {
	step := max(1, m.listHeight()/max(1, cardRows+1)/2)
	for i := 0; i < step; i++ {
		m.move(dir)
	}
}

func (m *Model) overlayKey(msg tea.KeyMsg) tea.Cmd {
	switch m.ov.kind {
	case ovHelp:
		room := max(1, m.h-4-7)
		switch msg.String() {
		case "j", "down", "ctrl+n":
			m.ov.cursor++
		case "k", "up", "ctrl+p":
			m.ov.cursor--
		case "pgdown", "ctrl+f", " ", "ctrl+d":
			m.ov.cursor += room
		case "pgup", "ctrl+b", "b", "ctrl+u":
			m.ov.cursor -= room
		case "g", "home":
			m.ov.cursor = 0
		case "G", "end":
			m.ov.cursor = 1 << 30
		default:
			m.closeOverlay()
		}
		return nil
	case ovSettings:
		return m.settingsKey(msg)
	case ovEdit:
		return m.editKey(msg)
	case ovConfirm:
		m.confirmKey(msg.String())
		return nil
	case ovMessage:
		room := max(1, m.h-4-6)
		switch msg.String() {
		case "esc", "q", "enter":
			m.closeOverlay()
		case "j", "down", "ctrl+n":
			m.ov.cursor++
		case "k", "up", "ctrl+p":
			m.ov.cursor--
		case "pgdown", "ctrl+f", " ":
			m.ov.cursor += room
		case "pgup", "ctrl+b", "b":
			m.ov.cursor -= room
		case "ctrl+d":
			m.ov.cursor += room / 2
		case "ctrl+u":
			m.ov.cursor -= room / 2
		case "g", "home":
			m.ov.cursor = 0
		case "G", "end":
			m.ov.cursor = len(m.ov.lines)
		case "left", "h", "K", "ctrl+k":
			return m.stepMessage(-1)
		case "right", "l", "J", "ctrl+j":
			return m.stepMessage(1)
		case "y":
			m.copyMessage()
		}
		return nil
	case ovResume:
		if m.ov.editing {
			switch msg.Type {
			case tea.KeyEsc:
				m.ov.edit.SetValue(m.ov.rec.Title)
				fallthrough
			case tea.KeyEnter:
				m.ov.editing = false
				m.ov.edit.Blur()
				return nil
			}
			var cmd tea.Cmd
			m.ov.edit, cmd = m.ov.edit.Update(msg)
			return cmd
		}
		switch msg.String() {
		case "enter":
			if m.pressFocused() {
				return nil
			}
			if d, t := m.broken(m.ov.rec); d || t {
				if d {
					m.askMove()
				} else {
					m.askDelete()
				}
				return nil
			}
			m.doResume(false)
		case "left", "h", "shift+tab":
			m.moveFocus(-1)
		case "right", "l", "tab":
			m.moveFocus(1)
		case "M":
			m.askMove()
		case "D":
			m.askDelete()
		case "f", "*":
			m.closeOverlay()
			m.toggleFavorite()
		case "x", "ctrl+x":
			m.closeOverlay()
			m.toggleStatus(fav.StatusDone)
		case "a", "ctrl+a":
			m.closeOverlay()
			m.toggleArchive()
		case "t", "ctrl+t":
			m.doResume(true)
		case "y", "ctrl+y":
			m.copyResume()
		case "i":
			m.openIDE()
		case "c":
			m.openCode()
		case "e", "ctrl+e":
			m.editTitle()
		case "esc", "q":
			m.closeOverlay()
		}
		return nil
	}

	// letters type into the picker; navigate with arrows and ctrl+n/p
	switch msg.String() {
	case "esc":
		m.closeOverlay()
		return nil
	case "enter":
		if m.pressFocused() {
			return nil
		}
		if m.ov.browse != nil {
			m.toggleOrApply() // dir picker: descends into the highlighted child; only the "this directory" row selects
			return nil
		}
		m.applyPicker()
		return nil
	case "left", "shift+tab":
		m.moveFocus(-1)
		return nil
	case "right", "tab":
		if m.ov.browse != nil {
			m.descendDir()
			return nil
		}
		m.moveFocus(1)
		return nil
	case "M":
		if m.ov.browse != nil { // same as M outside: pressing it again selects the listed directory (a capital M in a path is typed lowercase)
			m.pickDir()
			return nil
		}
	case "down", "ctrl+n":
		if m.ov.cursor < len(m.ov.visible())-1 {
			m.ov.cursor++
		}
		m.ov.focus = -1
		return nil
	case "up", "ctrl+p":
		if m.ov.cursor > 0 {
			m.ov.cursor--
		}
		m.ov.focus = -1
		return nil
	case " ":
		if m.ov.multi {
			m.toggleOrApply()
			return nil
		}
	case "backspace": // single-select: backspace on empty resets to the default; multi-select would lose the checks, so only the buttons
		if m.ov.filter.Value() == "" && m.ov.browse == nil && !m.ov.multi {
			m.clearPicker()
			return nil
		}
	}

	var cmd tea.Cmd
	m.ov.filter, cmd = m.ov.filter.Update(msg)
	m.ov.cursor = 0
	if m.ov.browse != nil { // dir picker: highlight on a child, Enter descends; focus leaves the buttons
		m.ov.focus = -1
		if len(m.ov.visible()) > 1 {
			m.ov.cursor = 1
		}
	}
	return cmd
}

type wheelTickMsg struct{}

// wheelFrame: wheel events are collected and applied once per frame, so a flood costs one render, not thousands.
const wheelFrame = 16 * time.Millisecond

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		dir := 1
		if msg.Button == tea.MouseButtonWheelUp {
			dir = -1
		}
		m.lastWheel = time.Now()
		m.wheelPend += dir
		m.wheelX = msg.X
		m.reuseFrame = true
		if m.wheelTick {
			return nil
		}
		m.wheelTick = true
		return tea.Tick(wheelFrame, func(time.Time) tea.Msg { return wheelTickMsg{} })
	case tea.MouseButtonLeft:
		m.mouseSelect(msg)
		if msg.Action != tea.MouseActionPress {
			return nil
		}
		m.notice = ""
		if act := m.hit(msg.X, msg.Y); act != nil {
			act(m)
		}
	case tea.MouseButtonNone:
		if msg.Action == tea.MouseActionRelease || msg.Action == tea.MouseActionMotion {
			m.mouseSelect(msg) // some terminals report release as key none
		}
	}
	return nil
}

// isMouseFragment recognises SGR mouse sequences split by bubbletea's 256-byte reads ("[<64;33;12M") that arrive as keys; they are dropped.
func (m *Model) applyWheel() {
	n, x := m.wheelPend, m.wheelX
	m.wheelPend = 0
	dir := 1
	if n < 0 {
		dir, n = -1, -n
	}
	moved := false
	for i := 0; i < n; i++ {
		moved = m.wheel(dir, x) || moved
	}
	m.reuseFrame = !moved
	if traceQ != nil {
		tracef("wheel %+d x=%d acc=%d moved=%v cursor=%d ov=%d/%d %s", dir*n, x, m.wheelAcc, moved, m.cursor, m.ov.kind, m.ov.cursor, m.traceChat())
	}
}

func isMouseFragment(k tea.KeyMsg) bool {
	if k.Type != tea.KeyRunes {
		return false
	}
	if k.Alt && len(k.Runes) == 1 && k.Runes[0] == '[' {
		return true
	}
	semi, digit := false, false
	for _, r := range k.Runes {
		switch {
		case r == ';':
			semi = true
		case r >= '0' && r <= '9':
			digit = true
		case r == '[' || r == '<' || r == 'M' || r == 'm':
		default:
			return false
		}
	}
	return semi && digit // a lone ; is typed
}

// wheel: one step per wheelStep events, a direction change steps at once; the right pane scrolls the chat, the list moves the cursor
// (not the viewport, never into the chip row). Returns whether anything moved: unmoved events do not redraw.
func (m *Model) wheel(dir, x int) bool {
	if m.wheelAcc*dir < 0 {
		m.wheelAcc = dir * (m.wheelStep - 1)
	}
	if m.wheelAcc += dir; m.wheelAcc%m.wheelStep != 0 {
		return false
	}
	if !m.ov.active() && (m.detail || (m.twoColumn() && x >= m.listWidth())) {
		return m.wheelChat(dir)
	}
	if m.ov.kind == ovPicker {
		n := m.ov.cursor + dir
		if n >= 0 && n < len(m.ov.visible()) {
			m.ov.cursor = n
			return true
		}
		return false
	}
	if m.ov.kind == ovMessage || m.ov.kind == ovHelp {
		n := min(max(m.ov.cursor+dir*m.wheelStep, 0), m.ov.scrollMax)
		if n == m.ov.cursor {
			return false
		}
		m.ov.cursor = n
		return true
	}
	if m.ov.active() {
		return false
	}
	m.chipFocus = -1
	if m.hitsOpen() {
		before := m.msg.hl.cur
		m.pending = tea.Batch(m.pending, m.selectHit(before+dir))
		return m.msg.hl.cur != before
	}
	if dir < 0 && m.atTop() {
		return false
	}
	before := m.cursor
	m.move(dir)
	return m.cursor != before
}

func (m *Model) wheelChat(dir int) bool {
	r := m.current()
	p := m.probes[r]
	if r == nil || p == nil || len(p.msgs) == 0 || m.chatW <= 0 {
		return false
	}
	q := m.findQuery()
	m.chatFollow = false
	if dir > 0 {
		next, skip := m.chatScroll, m.chatSkip+1
		if skip >= len(m.chatBlock(p.msgs[m.chatScroll], m.chatW, q, false)) {
			next, skip = m.chatScroll+1, 0
		}
		if next < len(p.msgs) && m.chatFills(m.chatScroll, m.chatSkip) {
			m.chatScroll, m.chatSkip = next, skip
			return true
		}
		return false
	}
	if m.chatSkip > 0 {
		m.chatSkip--
	} else if m.chatScroll > 0 {
		m.chatScroll--
		m.chatSkip = len(m.chatBlock(p.msgs[m.chatScroll], m.chatW, q, false)) - 1
	} else {
		return false
	}
	return true
}

func (m *Model) chatFills(scroll, skip int) bool {
	p := m.probes[m.current()]
	if p == nil || m.chatRoom <= 0 {
		return true
	}
	avail := m.chatRoom - 2
	lines := -skip
	for i := scroll; i < len(p.msgs) && lines <= avail; i++ {
		lines += len(m.chatBlock(p.msgs[i], m.chatW, m.findQuery(), false))
	}
	return lines > avail
}

func (m *Model) askResume() {
	r := m.current()
	if r == nil {
		return
	}
	if m.inTrash() {
		m.flash(i18n.T("trash.in_trash_hint"))
		return
	}
	ti := textinput.New()
	ti.SetValue(r.Title)
	ti.CharLimit = 120
	plan, _ := capture.PlanResume(r, m.live, false) // plan.Spec is empty on error
	m.ov = overlay{kind: ovResume, rec: r, plan: plan, edit: ti, focus: -1}
}

func (m *Model) visibleRecs() []*fav.Rec {
	// candidates come from the whole scope, not the keyword-filtered list
	q := m.query()
	q.Tags, q.Words, q.Project, q.Provider = nil, nil, "", ""
	recs := m.store.Query(q)
	if q.All {
		for _, r := range m.unfav {
			if q.Match(r) {
				recs = append(recs, r)
			}
		}
	}
	return recs
}

func (m *Model) pickTags() {
	q := fav.Parse(m.search.Value())
	m.openPicker(i18n.T("picker.tags_title"), i18n.T("picker.tags_hint"), tagsOf(m.visibleRecs()), true, q.Tags,
		func(m *Model, chosen []string) {
			toks := make([]string, len(chosen))
			for i, c := range chosen {
				toks[i] = "#" + c
			}
			m.setQuery(toks, func(t string) bool { return strings.HasPrefix(t, "#") })
		})
}

func (m *Model) pickProjects() {
	q := fav.Parse(m.search.Value())
	items := append([]item{{name: "", label: i18n.T("picker.all")}}, projectsOf(m.visibleRecs())...)
	m.openPicker(i18n.T("picker.projects_title"), "", items, false, []string{q.Project},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 && chosen[0] != "" {
				toks = []string{"project:" + chosen[0]}
			}
			m.setQuery(toks, hasPrefix("project:"))
		})
}

func (m *Model) saveConfig() {
	if err := m.cfg.Save(); err != nil {
		m.flash(i18n.T("flash.settings_not_saved") + err.Error())
	}
}

func (m *Model) cycleProvider() {
	q := fav.Parse(m.search.Value())
	cycle := []string{""}
	for _, it := range providersOf(m.visibleRecs()) {
		cycle = append(cycle, it.name)
	}
	next := cycle[(indexOf(cycle, q.Provider)+1)%len(cycle)]
	var toks []string
	if next != "" {
		toks = []string{"provider:" + next}
	}
	m.setQuery(toks, hasPrefix("provider:"))
	m.refresh()
}

func (m *Model) pickDate() {
	q := fav.Parse(m.search.Value())
	var cur []string
	for _, t := range strings.Fields(m.search.Value()) {
		if hasPrefix("last:", "after:", "before:")(t) {
			cur = append(cur, t)
		}
	}
	current := strings.Join(cur, " ")
	items := datePresets(time.Now())
	known := false
	for _, it := range items {
		known = known || it.name == current
	}
	if !known && current != "" {
		items = append([]item{{name: current, label: i18n.T("picker.current") + timeLabel(q)}}, items...)
	}
	m.openPicker(i18n.T("picker.time_title"), i18n.T("picker.time_hint"), items, false, []string{current},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 {
				toks = strings.Fields(chosen[0])
			}
			m.setQuery(toks, hasPrefix("last:", "after:", "before:"))
		})
	m.ov.parse = func(s string) (item, bool) { return dateItem(s, time.Now()) }
	m.ov.filter.Placeholder = i18n.T("picker.time_placeholder")
}

// datePresets: item.name is the one or two qualifiers written into the query.
func datePresets(now time.Time) []item {
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local) }
	ymd := func(t time.Time) string { return t.Format("2006-01-02") }
	span := func(a, b time.Time) string { return "after:" + ymd(a) + " before:" + ymd(b) }
	today := day(now)
	wd := (int(today.Weekday()) + 6) % 7 // Monday = 0
	week := today.AddDate(0, 0, -wd)
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.Local)
	year := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.Local)
	return []item{
		{name: "", label: i18n.T("picker.all")},
		{name: "after:" + ymd(today), label: i18n.T("date.today")},
		{name: span(today.AddDate(0, 0, -1), today), label: i18n.T("date.yesterday")},
		{name: "after:" + ymd(today.AddDate(0, 0, -1)), label: i18n.T("date.last_2_days")},
		{name: "after:" + ymd(today.AddDate(0, 0, -2)), label: i18n.T("date.last_3_days")},
		{name: "after:" + ymd(week), label: i18n.T("date.this_week")},
		{name: span(week.AddDate(0, 0, -7), week), label: i18n.T("date.last_week")},
		{name: "last:7d", label: i18n.T("date.last_7_days")},
		{name: "last:14d", label: i18n.T("date.last_14_days")},
		{name: "last:30d", label: i18n.T("date.last_30_days")},
		{name: "after:" + ymd(month), label: i18n.T("date.this_month")},
		{name: span(month.AddDate(0, -1, 0), month), label: i18n.T("date.last_month")},
		{name: "last:90d", label: i18n.T("date.last_90_days")},
		{name: "last:180d", label: i18n.T("date.last_6_months")},
		{name: "after:" + ymd(year), label: i18n.T("date.this_year")},
	}
}

// dateItem turns a typed expression into an item: 09-01 / 2026-09-01 since that day, a..b range (b inclusive), ..b before, 7d / 2w / 3m recent.
func dateItem(s string, now time.Time) (item, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return item{}, false
	}
	if a, b, ok := strings.Cut(s, ".."); ok {
		var toks []string
		label := ""
		if a != "" {
			t, ok := fav.ParseDay(a, now)
			if !ok {
				return item{}, false
			}
			toks = append(toks, "after:"+t.Format("2006-01-02"))
			label = t.Format("01-02")
		}
		if b != "" {
			t, ok := fav.ParseDay(b, now)
			if !ok {
				return item{}, false
			}
			toks = append(toks, "before:"+t.AddDate(0, 0, 1).Format("2006-01-02"))
			label += ".." + t.Format("01-02")
		} else {
			label += ".."
		}
		if len(toks) == 0 {
			return item{}, false
		}
		return item{name: strings.Join(toks, " "), label: label}, true
	}
	if t, ok := fav.ParseDay(s, now); ok {
		return item{name: "after:" + t.Format("2006-01-02"), label: i18n.T("date.from") + t.Format("01-02")}, true
	}
	if _, ok := fav.ParseWhen(s, now); ok && !strings.Contains(s, "-") {
		return item{name: "last:" + s, label: i18n.T("date.last") + s}, true
	}
	return item{}, false
}

func (m *Model) setQuery(add []string, sameKind func(string) bool) {
	var kept []string
	for _, t := range strings.Fields(m.search.Value()) {
		if !sameKind(t) {
			kept = append(kept, t)
		}
	}
	m.search.SetValue(strings.TrimSpace(strings.Join(append(kept, add...), " ")))
}

func hasPrefix(prefixes ...string) func(string) bool {
	return func(t string) bool {
		low := strings.ToLower(t)
		for _, p := range prefixes {
			if strings.HasPrefix(low, p) {
				return true
			}
		}
		return false
	}
}

// toggleArchive: archive ⇄ unarchive, board status untouched.
func (m *Model) toggleArchive() {
	r := m.current()
	if r == nil {
		return
	}
	ok := m.edit(func(r *fav.Rec) {
		if r.Archived() {
			r.ArchivedAt = nil
			return
		}
		now := time.Now()
		r.ArchivedAt = &now
	})
	if !ok {
		return
	}
	if r.Archived() {
		m.flash(i18n.F("flash.archived", r.Title))
	} else {
		m.flash(i18n.T("flash.unarchived") + r.Title)
	}
}

func (m *Model) toggleStatus(target string) {
	r := m.current()
	if r == nil {
		return
	}
	ok := m.edit(func(r *fav.Rec) {
		if r.Status == target {
			r.Status = fav.StatusDoing
			return
		}
		r.Status = target
	})
	if !ok {
		return
	}
	if r.Status == target {
		m.flash(i18n.F("flash.status_set", render.StatusLabel(target), r.Title))
	} else {
		m.flash(i18n.T("flash.back_to_doing") + r.Title)
	}
}

package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/remote"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	mm, cmd := m.update(msg)
	if m.hosts != nil {
		cmd = tea.Batch(cmd, m.wakeHosts())
	}
	if m.noticeNew {
		m.noticeNew = false
		seq := m.noticeSeq
		cmd = tea.Batch(cmd, tea.Tick(noticeFor(m.notice), func(time.Time) tea.Msg { return noticeExpiredMsg{seq} }))
	}
	return mm, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case noticeExpiredMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.search.SetWidth(max(10, m.w-6))

	case tea.BackgroundColorMsg:
		setTheme(msg.IsDark())
		m.themed = true

	case uv.PrimaryDeviceAttributesEvent, themeTimeoutMsg: // no background reply came first: the terminal will not send one
		m.themed = true

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

	case tea.KeyPressMsg:
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

	case tea.PasteMsg:
		m.notice = ""
		cmd = m.paste(msg)

	case herdrDoneMsg:
		if msg.err != nil {
			m.flash(i18n.F("flash.herdr_failed", msg.err.Error()))
			break
		}
		if msg.resumed {
			if err := capture.MarkResumed(m.store, msg.rec); err != nil {
				m.flash(i18n.F("flash.count_not_saved", err))
			}
		}
		m.refresh()
		note := msg.msg
		if msg.warn != nil {
			note += "；" + msg.warn.Error()
		}
		m.flash(note)

	case peekMsg:
		return m, m.applyPeek(msg)
	case peekTickMsg:
		if m.ov.kind == ovPeek && m.ov.title == msg.pane {
			return m, peekRead(msg.pane)
		}
		return m, nil
	case peekSentMsg:
		if msg.err != nil {
			m.flash(msg.err.Error())
		} else {
			m.flash(i18n.F("peek.sent", msg.what))
		}
		return m, nil
	case handoffMsg:
		m.openHandoff(msg)
		return m, nil
	case handoffEditedMsg:
		if msg.err != nil {
			m.flash(i18n.F("handoff.failed", msg.err.Error()))
		}
		if m.ov.kind == ovHandoff {
			m.loadHandoff()
		}
		return m, nil
	case appProbedMsg:
		if m.ov.kind == ovResume && m.ov.focus < 0 {
			m.ov.app = m.appFirst(m.ov.rec, m.ov.plan)
		}
		return m, nil
	case appDoneMsg:
		if msg.err != nil {
			m.flash(msg.err.Error() + i18n.T("resume.app_fallback"))
			break
		}
		if err := capture.MarkResumed(m.store, msg.rec); err != nil {
			m.flash(i18n.F("flash.count_not_saved", err))
			break
		}
		m.refresh()
		m.flash(i18n.F("resume.opened_app", capture.AppName(msg.rec.Provider)))

	case liveMsg:
		m.applyLive(msg)
		cmd = tea.Batch(tea.Tick(liveEvery, func(time.Time) tea.Msg { return liveTickMsg{} }), m.readPulses())

	case pulseMsg:
		m.applyPulses(msg)

	case hostMsg:
		cmd = m.applyHost(msg)

	case hostTickMsg:
		cmd = m.hostTick(string(msg))

	case fullMsg:
		m.applyFull(msg)

	case liveTickMsg:
		cmd = tea.Batch(m.pollLive(), m.refreshChat())

	case refreshMsg:
		if remote.Stale(msg.page.Err) {
			cmd = m.reprobe(msg.rec)
			break
		}
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
		if !msg.once {
			cmd = tea.Tick(indexEvery, func(time.Time) tea.Msg { return indexTickMsg{} })
		}
		if msg.changed && msg.gen == m.idxGen {
			cmd = tea.Batch(cmd, m.syncText(msg.idx))
		}

	case indexTickMsg:
		cmd = m.refreshIndex()

	case probeMsg:
		tracef("probe %s msgs=%d cur=%v", msg.rec.SessionID, len(msg.page.Msgs), msg.rec == m.current())
		if remote.Stale(msg.page.Err) {
			cmd = m.reprobe(msg.rec)
		} else if p := m.probes[msg.rec]; p != nil {
			p.checks, p.msgs, p.from, p.full, p.done = msg.checks, msg.page.Msgs, msg.page.From, msg.page.Done, true
			p.failed = msg.page.Err != nil
			if m.findQuery() != "" && msg.rec == m.current() {
				cmd = tea.Batch(cmd, m.landHit())
			}
		}

	case pageMsg:
		tracef("page %s +%d done=%v cur=%v %s", msg.rec.SessionID, len(msg.page.Msgs), msg.page.Done, msg.rec == m.current(), m.traceChat())
		if remote.Stale(msg.page.Err) {
			cmd = m.reprobe(msg.rec)
			break
		}
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
		m.heldIdx, m.forced = nil, nil
	}
	m.applyFresh()
	// nearing the loaded end (under 10 left or the pane not full): fetch an older page
	if r := m.current(); r != nil {
		if p := m.probes[r]; p != nil && p.done && !p.full && !p.loading && !p.failed &&
			(m.chatScroll+m.chatShown >= len(p.msgs)-10 || !m.chatFills(m.chatScroll, m.chatSkip)) {
			tracef("older %s %s", r.SessionID, m.traceChat())
			cmd = tea.Batch(cmd, m.load(r, olderMsgs))
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
	resumed bool // a new tab resumed the session (not a focus, fork or new session): counted
	warn    error
	err     error
}

func (m *Model) finish(res Result) {
	m.result, m.quitting = res, true
}

// searchKey: letters type into the search box; arrows and paging move the list, Enter after a selection resumes.
func (m *Model) searchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.typing = false
		m.search.Blur()
		m.refresh()
		return nil
	case "enter":
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
	case "up", "down", "pgup", "pgdown", "ctrl+n", "ctrl+p":
		m.moved = true
		return m.navKey(msg)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.refresh()
	return cmd
}

// navKey: list navigation; keys come from the table in keys.go. ⚠️ Every action needs a non-letter key (letters never
// reach here under a CJK IME).
func (m *Model) navKey(msg tea.KeyPressMsg) tea.Cmd {
	a := keyAct(inList, msg.String())
	if m.inTrash() && m.current() != nil && m.chipFocus < 0 && trashBlocked(a) {
		m.flash(i18n.T("trash.in_trash_hint"))
		return nil
	}
	if remoteBlocked(a) && m.remoteRow() { // whatever has focus, these act on the current row
		m.flash(i18n.T("remote.read_only"))
		return nil
	}
	if m.hitsOpen() && m.pane == paneList && m.chipFocus < 0 {
		if cmd, ok := m.hitKey(a); ok {
			return cmd
		}
	}
	switch a {
	case actFind:
		return m.startChatSearch()
	case actNextHit:
		if m.hitsOpen() {
			return m.selectHit(m.msg.hl.cur + 1)
		}
		return m.findHit(m.chat.cur + 1)
	case actPrevHit:
		if m.hitsOpen() {
			return m.selectHit(m.msg.hl.cur - 1)
		}
		m.jumpHit(m.chat.cur - 1)
	case actQuit:
		m.quitting = true
	case actBack:
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
	case actSearch:
		m.focusSearch()
	case actMsgSearch:
		m.focusMsgSearch()
	case actDown:
		switch {
		case m.projectFocus():
			m.projCur++
		case m.pane == paneChat:
			m.moveChat(1)
		default:
			m.move(1)
		}
	case actUp:
		switch {
		case m.projectFocus():
			m.projCur--
		case m.pane == paneChat:
			m.moveChat(-1)
		default:
			m.move(-1)
		}
	case actChatDown:
		m.moveChat(1)
	case actChatUp:
		m.moveChat(-1)
	case actLeft: // ← backs out: chip row → right pane → narrow detail → collapse group
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
	case actRight: // → goes in: expand group → right pane → full message
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
	case actCopy:
		if m.pane == paneChat {
			m.copyMessage()
		}
	case actTop:
		if m.pane == paneChat {
			m.moveChat(-1 << 30)
		} else {
			m.cursor = 0
			m.clampCursor()
		}
	case actBottom:
		if m.pane == paneChat {
			m.moveChat(1 << 30)
		} else {
			m.cursor = len(m.rows) - 1
			m.clampCursor()
		}
	case actPageDown:
		if m.pane == paneChat {
			m.moveChat(5)
		} else {
			m.page(1, 1)
		}
	case actPageUp:
		if m.pane == paneChat {
			m.moveChat(-5)
		} else {
			m.page(-1, 1)
		}
	case actHalfDown:
		if m.pane == paneChat {
			m.moveChat(3)
		} else {
			m.page(1, 2)
		}
	case actHalfUp:
		if m.pane == paneChat {
			m.moveChat(-3)
		} else {
			m.page(-1, 2)
		}
	case actNextView:
		// switching views keeps the query
		m.setView(m.nextView())
	case actPrevView:
		m.setView(view((int(m.view) + viewCount - 1) % viewCount))
	case actView:
		m.setView(view(int(msg.String()[0] - '1')))
	case actChips:
		if m.chipFocus >= 0 {
			m.chipFocus = -1
		} else {
			m.chipFocus = 0
		}
	case actFavorite:
		m.toggleFavorite(m.current())
	case actHandled:
		m.handleAttn(m.current(), false)
	case actSnooze:
		m.handleAttn(m.current(), true)
	case actSpace: // switches to a session already in a Herdr tab; anything that would start a process goes through the dialog
		if m.pane == paneChat && m.current() != nil {
			m.moveChat(5)
			return nil
		}
		if r := m.current(); r != nil && r.Host == "" {
			if l, ok := m.live[r.SessionID]; ok && l.TabID != "" {
				p, _ := capture.PlanResume(r, m.live, false)
				m.runPlan(r, p, false)
				return nil
			}
		}
		return m.navKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	case actFoldAll:
		if m.view == viewProjects {
			m.foldAll(nil)
		}
	case actFold, actUnfold:
		if m.view == viewProjects {
			fold := a == actFold
			m.foldAll(&fold)
		}
	case actEnter:
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
	case actCloseTab:
		m.closeLive(m.current())
	case actCloseIdle:
		if m.view == viewLive {
			m.closeIdle()
		}
	case actResume:
		m.askResume()
	case actTags:
		m.pickTags()
	case actProjects:
		m.pickProjects()
	case actNew:
		m.askStart(m.current())
	case actProvider:
		m.cycleProvider()
	case actHost:
		m.pickHost()
	case actPeek:
		m.askPeek(m.current())
	case actStatus:
		m.pickStatus()
	case actDelete:
		m.askDelete(m.current())
	case actMove:
		m.askMove(m.current())
	case actDate:
		m.pickDate()
	case actSort:
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
	case actArchive:
		m.toggleArchive(m.current())
	case actDone:
		m.toggleStatus(m.current(), fav.StatusDone)
	case actEdit:
		return m.openEdit(m.current())
	case actHelp:
		m.ov = overlay{kind: ovHelp, focus: -1}
	case actSettings:
		m.openSettings()
	}
	return nil
}

// page moves the list cursor a screen of cards (div 2: half a screen).
func (m *Model) page(dir, div int) {
	for range max(1, m.listHeight()/(cardRows+1)/div) {
		m.move(dir)
	}
}

func (m *Model) overlayKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.ov.kind {
	case ovHelp:
		switch a := keyAct(inReader, msg.String()); a {
		case actTabNext:
			m.turnHelpPage(1)
		case actTabPrev:
			m.turnHelpPage(-1)
		default:
			if !m.scrollKey(a) {
				m.closeOverlay()
			}
		}
		return nil
	case ovSettings:
		return m.settingsKey(msg)
	case ovEdit:
		return m.editKey(msg)
	case ovConfirm:
		m.confirmKey(msg.String())
		return nil
	case ovHandoff:
		return m.handoffKey(msg)
	case ovStart:
		return m.startKey(msg)
	case ovPeek:
		return m.peekKey(msg)
	case ovMessage:
		switch a := keyAct(inReader, msg.String()); a {
		case actClose:
			m.closeOverlay()
		case actTabPrev:
			return m.stepMessage(-1)
		case actTabNext:
			return m.stepMessage(1)
		case actCopy:
			m.copyMessage()
		default:
			m.scrollKey(a)
		}
		return nil
	case ovResume:
		if m.ov.editing {
			switch msg.String() {
			case "esc":
				m.ov.edit.SetValue(m.ov.rec.Title)
				fallthrough
			case "enter":
				m.ov.editing = false
				m.ov.edit.Blur()
				return nil
			}
			var cmd tea.Cmd
			m.ov.edit, cmd = m.ov.edit.Update(msg)
			return cmd
		}
		a := keyAct(inResume, msg.String())
		if m.ov.rec.Host != "" && remoteBlocked(a) {
			m.flash(i18n.T("remote.read_only"))
			return nil
		}
		if b := bindingOf(inResume, a); b != nil && b.tier == tierStart && a != actResume {
			m.focusKey(a) // its meaning differs from the list: the first press only focuses the button
			return nil
		}
		switch a {
		case actResume:
			m.doResume(false)
		case actMove, actDelete, actFavorite, actDone, actArchive, actEdit:
			m.recordAction(a)
		case actCopy:
			m.copyResume()
		case actTitle:
			m.editTitle()
		case actNew:
			m.askStartFromDialog()
		case actPeek:
			if m.ov.plan.Live.PaneID != "" {
				m.askPeek(m.ovRec())
			}
		case actHandled, actSnooze, actCloseTab:
			m.agentAction(a)
		default:
			m.dialogKey(a, m.resumeEnter)
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
	case "right":
		if m.ov.browse != nil {
			m.descendDir()
			return nil
		}
		m.moveFocus(1)
		return nil
	case "tab":
		m.moveFocus(1)
		return nil
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
	case "space":
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
	m.filterChanged()
	return cmd
}

func (m *Model) filterChanged() {
	m.ov.cursor = 0
	if m.ov.browse != nil { // dir picker: highlight on a child, Enter descends; focus leaves the buttons
		m.ov.focus = -1
		if len(m.ov.visible()) > 1 {
			m.ov.cursor = 1
		}
	}
}

// paste: a paste arrives as its own message and only ever types into the focused input (unfocused inputs ignore it).
func (m *Model) paste(msg tea.PasteMsg) tea.Cmd {
	var c1, c2, c3, c4 tea.Cmd
	switch {
	case m.ov.active():
		m.ov.edit, c1 = m.ov.edit.Update(msg)
		m.ov.edit2, c2 = m.ov.edit2.Update(msg)
		m.ov.area, c3 = m.ov.area.Update(msg)
		if m.ov.filter.Focused() {
			m.ov.filter, c4 = m.ov.filter.Update(msg)
			m.filterChanged()
		}
	case m.chat.typing:
		m.chat.input, c1 = m.chat.input.Update(msg)
		m.hitsFor = nil
	case m.typing:
		m.search, c1 = m.search.Update(msg)
		m.refresh()
	}
	return tea.Batch(c1, c2, c3, c4)
}

type wheelTickMsg struct{}

// wheelFrame: wheel events are collected and applied once per frame, so a flood costs one render, not thousands.
const wheelFrame = 16 * time.Millisecond

func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	mo := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		if mo.Button != tea.MouseWheelUp && mo.Button != tea.MouseWheelDown {
			return nil
		}
		dir := 1
		if mo.Button == tea.MouseWheelUp {
			dir = -1
		}
		m.lastWheel = time.Now()
		m.wheelPend += dir
		m.wheelX = mo.X
		m.reuseFrame = true
		if m.wheelTick {
			return nil
		}
		m.wheelTick = true
		return tea.Tick(wheelFrame, func(time.Time) tea.Msg { return wheelTickMsg{} })
	case tea.MouseClickMsg:
		if mo.Button != tea.MouseLeft {
			return nil
		}
		m.mouseSelect(msg)
		m.notice = ""
		if act := m.hit(mo.X, mo.Y); act != nil {
			act(m)
		}
	case tea.MouseMotionMsg, tea.MouseReleaseMsg:
		if mo.Button == tea.MouseLeft || mo.Button == tea.MouseNone {
			m.mouseSelect(msg)
		}
	}
	return nil
}

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
	if m.ov.kind == ovMessage || m.ov.kind == ovHelp || m.ov.kind == ovHandoff {
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

func (m *Model) askResume() { m.openResume(m.current()) }

// resumeEnter: a broken session goes to move / delete, else resume (in the desktop app when that leads).
func (m *Model) resumeEnter() {
	r := m.ovRec()
	switch d, t := m.broken(r); {
	case d:
		m.askMove(r)
	case t:
		m.askDelete(r)
	case m.ov.app:
		m.doApp()
	default:
		m.doResume(false)
	}
}

func (m *Model) openResume(r *fav.Rec) {
	if r == nil {
		return
	}
	if m.inTrash() {
		m.flash(i18n.T("trash.in_trash_hint"))
		return
	}
	if r.Host != "" {
		m.openRemoteResume(r)
		return
	}
	ti := newInput()
	ti.SetValue(r.Title)
	ti.CharLimit = 120
	plan, _ := capture.PlanResume(r, m.live, false) // plan.Spec is empty on error
	m.ov = overlay{kind: ovResume, rec: r, plan: plan, edit: ti, focus: -1, app: m.appFirst(r, plan)}
	if cmd := probeApp(r); cmd != nil {
		m.pending = tea.Batch(m.pending, cmd)
	}
}

func (m *Model) visibleRecs() []*fav.Rec { return m.list(m.query().Scope()) }

func (m *Model) pickTags() {
	q := fav.Parse(m.search.Value())
	m.openPicker(i18n.T("picker.tags_title"), i18n.T("picker.tags_hint"), tagsOf(m.visibleRecs()), true, q.Tags,
		func(m *Model, chosen []string) {
			toks := make([]string, len(chosen))
			for i, c := range chosen {
				toks[i] = "#" + c
			}
			m.setQuery(toks, fav.HasPrefix("#"))
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
			m.setQuery(toks, fav.HasPrefix("project:"))
		})
}

func (m *Model) saveConfig() {
	if err := m.cfg.Save(); err != nil {
		m.flash(i18n.F("flash.settings_not_saved", err))
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
	m.setQuery(toks, fav.HasPrefix("provider:"))
	m.refresh()
}

func (m *Model) pickDate() {
	q := fav.Parse(m.search.Value())
	var cur []string
	for t := range strings.FieldsSeq(m.search.Value()) {
		if fav.HasPrefix("last:", "after:", "before:")(t) {
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
		items = append([]item{{name: current, label: i18n.F("picker.current", timeLabel(q))}}, items...)
	}
	m.openPicker(i18n.T("picker.time_title"), i18n.T("picker.time_hint"), items, false, []string{current},
		func(m *Model, chosen []string) {
			var toks []string
			if len(chosen) > 0 {
				toks = strings.Fields(chosen[0])
			}
			m.setQuery(toks, fav.HasPrefix("last:", "after:", "before:"))
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
		{name: "last:" + ymd(today), label: i18n.T("date.today")},
		{name: span(today.AddDate(0, 0, -1), today), label: i18n.T("date.yesterday")},
		{name: "last:" + ymd(today.AddDate(0, 0, -1)), label: i18n.T("date.last_2_days")},
		{name: "last:" + ymd(today.AddDate(0, 0, -2)), label: i18n.T("date.last_3_days")},
		{name: "last:" + ymd(week), label: i18n.T("date.this_week")},
		{name: span(week.AddDate(0, 0, -7), week), label: i18n.T("date.last_week")},
		{name: "last:7d", label: i18n.T("date.last_7_days")},
		{name: "last:14d", label: i18n.T("date.last_14_days")},
		{name: "last:30d", label: i18n.T("date.last_30_days")},
		{name: "last:" + ymd(month), label: i18n.T("date.this_month")},
		{name: span(month.AddDate(0, -1, 0), month), label: i18n.T("date.last_month")},
		{name: "last:90d", label: i18n.T("date.last_90_days")},
		{name: "last:180d", label: i18n.T("date.last_6_months")},
		{name: "last:" + ymd(year), label: i18n.T("date.this_year")},
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
		return item{name: "last:" + t.Format("2006-01-02"), label: i18n.F("date.active_from", t.Format("01-02"))}, true
	}
	if _, ok := fav.ParseWhen(s, now); ok && !strings.Contains(s, "-") {
		return item{name: "last:" + s, label: i18n.F("date.last", s)}, true
	}
	return item{}, false
}

func (m *Model) setQuery(add []string, sameKind func(string) bool) {
	m.search.SetValue(fav.ReplaceTokens(m.search.Value(), add, sameKind))
}

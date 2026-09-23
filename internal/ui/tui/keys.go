package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// scope is where a binding is live; one bit per key context.
type scope uint16

const (
	inList    scope = 1 << iota // main screen: list, right pane, hits
	inResume                    // the resume dialog (Enter on a session)
	inConfirm                   // yes / cancel confirmations
	inStart                     // new-session dialog
	inHandoff                   // handoff dialog
	inPeek                      // peek at a Herdr agent (outside its reply input)
)

var scopes = []scope{inList, inResume, inConfirm, inStart, inHandoff, inPeek}

// tier is how much an action changes. ⚠️ Rules for new keys:
// tierStart never gets a list key (only a dialog's Enter or the same key pressed twice runs it);
// tierHeavy is always confirmed.
type tier uint8

const (
	tierNav    tier = iota // moves, filters, opens a view or a dialog
	tierRecord             // reversible change to fav's own records
	tierStart              // starts a process, opens a tab or an app, sends to an agent
	tierHeavy              // cannot be undone from fav
)

type act uint8

const (
	actNone act = iota
	actSearch
	actMsgSearch
	actFind
	actNextHit
	actPrevHit
	actSort
	actDown
	actUp
	actPageDown
	actPageUp
	actHalfDown
	actHalfUp
	actTop
	actBottom
	actLeft
	actRight
	actChatDown
	actChatUp
	actEnter
	actSpace
	actCopy
	actNextView
	actPrevView
	actView
	actChips
	actTags
	actProjects
	actProvider
	actDate
	actStatus
	actFoldAll
	actFold
	actUnfold
	actFavorite
	actDone
	actArchive
	actEdit
	actMove
	actDelete
	actResume
	actNew
	actPeek
	actHandled
	actSnooze
	actCloseTab
	actCloseIdle
	actHelp
	actSettings
	actBack
	actQuit
	actTerminal
	actApp
	actFork
	actHandoff
	actIDE
	actCode
	actFiles
	actTitle
	actFocusPrev
	actFocusNext
	actClose
	actConfirm
	actClaude
	actCodex
	actAnswer
	actReply
	actTabPrev
	actTabNext
	// actViaDialog is not a key: an ime route meaning "a button in the resume dialog".
	actViaDialog
)

// binding: keys[0] is the one shown; the rest are aliases — Ctrl for the IME, punctuation in both widths.
// ime names the non-letter route for a binding whose keys are all lowercase letters (an IME swallows them).
type binding struct {
	act  act
	in   scope
	tier tier
	keys []string
	ime  act
}

var bindings = []binding{
	{act: actSearch, in: inList, keys: []string{"/", "、"}}, // a CJK input method types / as 、
	{act: actMsgSearch, in: inList, keys: []string{">", "》"}},
	{act: actFind, in: inList, keys: []string{"ctrl+s", "\\"}},
	{act: actNextHit, in: inList, keys: []string{"n"}, ime: actDown},
	{act: actPrevHit, in: inList, keys: []string{"N"}},
	{act: actSort, in: inList, keys: []string{"o", "ctrl+o"}},
	{act: actDown, in: inList, keys: []string{"j", "down", "ctrl+n"}},
	{act: actUp, in: inList, keys: []string{"k", "up", "ctrl+p"}},
	{act: actPageDown, in: inList, keys: []string{"pgdown", "ctrl+f"}},
	{act: actPageUp, in: inList, keys: []string{"pgup", "ctrl+b"}},
	{act: actHalfDown, in: inList, keys: []string{"ctrl+d"}},
	{act: actHalfUp, in: inList, keys: []string{"ctrl+u"}},
	{act: actTop, in: inList, keys: []string{"g", "home"}},
	{act: actBottom, in: inList, keys: []string{"G", "end"}},
	{act: actLeft, in: inList, keys: []string{"h", "left"}},
	{act: actRight, in: inList, keys: []string{"l", "right"}},
	{act: actChatDown, in: inList, keys: []string{"J", "ctrl+j"}},
	{act: actChatUp, in: inList, keys: []string{"K", "ctrl+k"}},
	{act: actEnter, in: inList, keys: []string{"enter"}},
	{act: actSpace, in: inList, keys: []string{"space", "ctrl+g"}},
	{act: actNextView, in: inList, keys: []string{"tab"}},
	{act: actPrevView, in: inList, keys: []string{"shift+tab"}},
	{act: actView, in: inList, keys: []string{"1", "2", "3", "4"}},
	{act: actChips, in: inList, keys: []string{";", "；"}},
	{act: actTags, in: inList, keys: []string{"t"}, ime: actChips},
	{act: actProjects, in: inList, keys: []string{"p"}, ime: actChips},
	{act: actProvider, in: inList, keys: []string{"v"}, ime: actChips},
	{act: actDate, in: inList, keys: []string{"d"}, ime: actChips},
	{act: actStatus, in: inList, keys: []string{"s"}, ime: actChips},
	{act: actFoldAll, in: inList, keys: []string{"z"}, ime: actFold},
	{act: actFold, in: inList, keys: []string{"-"}},
	{act: actUnfold, in: inList, keys: []string{"=", "+"}},
	{act: actResume, in: inList, keys: []string{"r"}, ime: actEnter},
	{act: actNew, in: inList | inResume, keys: []string{"w", "ctrl+w"}},
	{act: actFavorite, in: inList | inResume, tier: tierRecord, keys: []string{"f", "*", "＊"}},
	{act: actDone, in: inList | inResume, tier: tierRecord, keys: []string{"x", "ctrl+x"}},
	{act: actArchive, in: inList | inResume, tier: tierRecord, keys: []string{"a"}, ime: actViaDialog}, // ⚠️ never Ctrl+A: Herdr's prefix
	{act: actEdit, in: inList | inResume, tier: tierRecord, keys: []string{"e", "ctrl+e"}},
	{act: actMove, in: inList | inResume, tier: tierHeavy, keys: []string{"M"}},
	{act: actDelete, in: inList | inResume, tier: tierHeavy, keys: []string{"D"}},
	{act: actCopy, in: inList | inResume, keys: []string{"y", "ctrl+y"}},
	{act: actPeek, in: inList | inResume, keys: []string{"`", "·", "｀"}}, // an IME types ` as ·
	{act: actHandled, in: inList | inResume, tier: tierRecord, keys: []string{".", "。"}},
	{act: actSnooze, in: inList | inResume, tier: tierRecord, keys: []string{"H"}},
	{act: actCloseTab, in: inList | inResume, tier: tierHeavy, keys: []string{"X"}},
	{act: actCloseIdle, in: inList, tier: tierHeavy, keys: []string{"Z"}},
	{act: actHelp, in: inList, keys: []string{"?", "？"}},
	{act: actSettings, in: inList, keys: []string{",", "，"}},
	{act: actBack, in: inList, keys: []string{"esc"}},
	{act: actQuit, in: inList, keys: []string{"q", "ctrl+c"}},

	{act: actEnter, in: inResume | inConfirm | inStart | inHandoff | inPeek, keys: []string{"enter"}},
	{act: actResume, in: inResume, tier: tierStart, keys: []string{"r"}}, // the dialog is the confirmation: r = Enter
	{act: actTerminal, in: inResume, tier: tierStart, keys: []string{"t", "ctrl+t"}},
	{act: actApp, in: inResume, tier: tierStart, keys: []string{"p"}},
	{act: actFork, in: inResume, tier: tierStart, keys: []string{"b"}},
	{act: actHandoff, in: inResume, tier: tierStart, keys: []string{"s"}},
	{act: actIDE, in: inResume, tier: tierStart, keys: []string{"i"}},
	{act: actCode, in: inResume, tier: tierStart, keys: []string{"c"}},
	{act: actFiles, in: inResume, tier: tierStart, keys: []string{"o"}},
	{act: actTitle, in: inResume, keys: []string{"n"}},
	{act: actFocusPrev, in: inResume | inConfirm | inStart | inHandoff, keys: []string{"shift+tab", "left", "h"}},
	{act: actFocusNext, in: inResume | inConfirm | inStart | inHandoff, keys: []string{"tab", "right", "l"}},
	{act: actClose, in: inResume | inStart | inHandoff | inPeek, keys: []string{"esc", "q"}},

	{act: actConfirm, in: inConfirm, tier: tierHeavy, keys: []string{"y"}},
	{act: actClose, in: inConfirm, keys: []string{"esc", "q", "n"}},

	{act: actClaude, in: inStart | inHandoff, tier: tierStart, keys: []string{"1"}},
	{act: actCodex, in: inStart | inHandoff, tier: tierStart, keys: []string{"2"}},
	{act: actDown, in: inStart | inHandoff, keys: []string{"j", "down", "ctrl+n"}},
	{act: actUp, in: inStart | inHandoff, keys: []string{"k", "up", "ctrl+p"}},
	{act: actPageDown, in: inHandoff, keys: []string{"space", "pgdown", "ctrl+f"}},
	{act: actPageUp, in: inHandoff, keys: []string{"pgup", "ctrl+b"}},
	{act: actEdit, in: inHandoff, keys: []string{"e", "ctrl+e"}},
	{act: actCopy, in: inHandoff, keys: []string{"y", "ctrl+y"}},

	{act: actAnswer, in: inPeek, tier: tierStart, keys: []string{"1", "2", "3"}},
	{act: actReply, in: inPeek, keys: []string{":", "："}},
	{act: actTabNext, in: inPeek, keys: []string{"tab"}},
	{act: actTabPrev, in: inPeek, keys: []string{"shift+tab"}},
	{act: actFocusPrev, in: inPeek, keys: []string{"left", "h"}},
	{act: actFocusNext, in: inPeek, keys: []string{"right", "l"}},
}

var keyIndex = func() map[scope]map[string]*binding {
	idx := map[scope]map[string]*binding{}
	for _, s := range scopes {
		idx[s] = map[string]*binding{}
	}
	for i := range bindings {
		b := &bindings[i]
		for _, s := range scopes {
			if b.in&s == 0 {
				continue
			}
			for _, k := range b.keys {
				idx[s][k] = b
			}
		}
	}
	return idx
}()

// keyAct: what key k does in scope s.
func keyAct(s scope, k string) act {
	if b := keyIndex[s][k]; b != nil {
		return b.act
	}
	return actNone
}

func bindingOf(s scope, a act) *binding {
	for i := range bindings {
		if bindings[i].act == a && bindings[i].in&s != 0 {
			return &bindings[i]
		}
	}
	return nil
}

// keyOf: the key shown for a in s.
func keyOf(s scope, a act) string {
	if b := bindingOf(s, a); b != nil {
		return keyName(b.keys[0])
	}
	return ""
}

// footKeyOf: a lowercase letter is shown with its IME-safe punctuation alias ("f/*") or replaced by its Ctrl key.
func footKeyOf(s scope, a act) string {
	b := bindingOf(s, a)
	if b == nil {
		return ""
	}
	k := keyName(b.keys[0])
	if !isLower(b.keys[0]) {
		return k
	}
	for _, alt := range b.keys[1:] {
		if utf8.RuneCountInString(alt) == 1 && !isWide(alt) {
			return k + "/" + alt
		}
	}
	for _, alt := range b.keys[1:] {
		if strings.HasPrefix(alt, "ctrl+") {
			return keyName(alt)
		}
	}
	return k
}

// keyed: a button or footer label, key first ("f 收藏"); labelKey splits it again for colouring.
func keyed(key, text string) string {
	if key == "" {
		return text
	}
	return key + " " + text
}

var keyNamesShown = map[string]string{
	"space": "Space", "enter": "Enter", "esc": "Esc", "tab": "Tab", "shift+tab": "Shift+Tab", "backspace": "Backspace",
	"up": "↑", "down": "↓", "left": "←", "right": "→", "pgup": "PgUp", "pgdown": "PgDn", "home": "Home", "end": "End",
}

// keyName: bubbletea's key string as the user reads it (ctrl+x → Ctrl+X).
func keyName(k string) string {
	if n, ok := keyNamesShown[k]; ok {
		return n
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok {
		return "Ctrl+" + strings.ToUpper(rest)
	}
	return k
}

func isLower(k string) bool {
	r, n := utf8.DecodeRuneInString(k)
	return n == len(k) && r < utf8.RuneSelf && unicode.IsLower(r)
}

func isLetter(k string) bool {
	r, n := utf8.DecodeRuneInString(k)
	return n == len(k) && r < utf8.RuneSelf && unicode.IsLetter(r)
}

// isWide: a full-width ASCII form (＊ ； ，…); accepted, never shown when its narrow twin is.
func isWide(k string) bool {
	r, n := utf8.DecodeRuneInString(k)
	return n == len(k) && r >= 0xFF01 && r <= 0xFF5E
}

// shownKeys: the keys of b a reader needs, in table order.
func shownKeys(b *binding) []string {
	var out []string
	for _, k := range b.keys {
		if !isWide(k) {
			out = append(out, keyName(k))
		}
	}
	return out
}

// helpSpec is one help row: the keys of acts in scope s, drawn in pairs ("j / k") when pair is set.
type helpSpec struct {
	desc string
	s    scope
	pair bool
	acts []act
}

type helpSection struct {
	title string
	note  string
	rows  []helpSpec
}

func helpLayout() []helpSection {
	return []helpSection{
		{"help.group.search", "", []helpSpec{
			{"help.search", inList, false, []act{actSearch}},
			{"help.msg_search", inList, false, []act{actMsgSearch}},
			{"help.find", inList, false, []act{actFind}},
			{"help.next_hit", inList, true, []act{actNextHit, actPrevHit}},
			{"help.all_hits", inList, false, []act{actRight}},
			{"help.sort", inList, false, []act{actSort}},
		}},
		{"help.group.read", "", []helpSpec{
			{"help.move", inList, true, []act{actDown, actUp}},
			{"help.page", inList, true, []act{actPageDown, actPageUp, actHalfDown, actHalfUp, actTop, actBottom}},
			{"help.pane", inList, true, []act{actLeft, actRight}},
			{"help.tabs", inList, true, []act{actNextView, actPrevView, actView}},
		}},
		{"help.group.view", "", []helpSpec{
			{"help.chip_row", inList, false, []act{actChips}},
			{"help.filters", inList, false, []act{actTags, actProjects, actProvider, actDate}},
			{"help.status", inList, false, []act{actStatus}},
			{"help.enter_group", inList, false, []act{actEnter}},
			{"help.fold_all", inList, false, []act{actFoldAll, actFold, actUnfold}},
		}},
		{"help.group.open", "", []helpSpec{
			{"help.enter", inList, false, []act{actEnter, actResume}},
			{"help.space", inList, false, []act{actSpace}},
			{"help.new_session", inList, false, []act{actNew}},
		}},
		{"help.group.record", "", []helpSpec{
			{"help.favorite", inList, false, []act{actFavorite}},
			{"help.done", inList, false, []act{actDone}},
			{"help.archive", inList, false, []act{actArchive}},
			{"help.edit", inList, false, []act{actEdit}},
			{"help.move_project", inList, false, []act{actMove}},
			{"help.delete", inList, false, []act{actDelete}},
		}},
		{"help.group.chat", "", []helpSpec{
			{"help.chat_move", inList, true, []act{actChatDown, actChatUp}},
			{"help.enter_chat", inList, false, []act{actEnter}},
			{"help.copy_msg", inList, false, []act{actCopy}},
		}},
		{"help.group.agents", "", []helpSpec{
			{"help.peek", inList, false, []act{actPeek}},
			{"help.handled", inList, false, []act{actHandled}},
			{"help.snooze", inList, false, []act{actSnooze}},
			{"help.close_tab", inList, false, []act{actCloseTab}},
			{"help.close_idle", inList, false, []act{actCloseIdle}},
		}},
		{"help.group.resume", "help.resume_note", []helpSpec{
			{"help.r_resume", inResume, false, []act{actResume}},
			{"help.t_resume", inResume, false, []act{actTerminal}},
			{"help.app", inResume, false, []act{actApp}},
			{"help.y_resume", inResume, false, []act{actCopy}},
			{"help.fork", inResume, false, []act{actFork}},
			{"help.handoff", inResume, false, []act{actHandoff}},
			{"help.ide", inResume, false, []act{actIDE, actCode, actFiles}},
			{"help.edit_title", inResume, false, []act{actTitle}},
		}},
		{"help.group.dialog", "", []helpSpec{
			{"help.dlg_focus", inConfirm, true, []act{actFocusNext, actFocusPrev}},
			{"help.dlg_enter", inConfirm, false, []act{actEnter}},
			{"help.dlg_close", inConfirm, false, []act{actClose}},
			{"help.dlg_confirm", inConfirm, false, []act{actConfirm}},
			{"help.dlg_provider", inStart, true, []act{actClaude, actCodex}},
			{"help.dlg_pack", inHandoff, false, []act{actEdit, actCopy}},
			{"help.dlg_answer", inPeek, false, []act{actAnswer}},
			{"help.dlg_reply", inPeek, false, []act{actReply}},
		}},
		{"help.group.other", "", []helpSpec{
			{"help.settings", inList, false, []act{actSettings}},
			{"help.help", inList, false, []act{actHelp}},
			{"help.esc", inList, false, []act{actBack}},
			{"help.quit", inList, false, []act{actQuit}},
		}},
	}
}

// helpKeys: the key column of a row, at most w wide per line. Paired acts zip their keys ("j / k", "↓ / ↑"); other
// acts are joined by " / " when that fits, else one per line; one act's keys are never split unless they alone overflow.
func helpKeys(h helpSpec, w int) []string {
	var groups []string
	pack := func(parts []string) {
		for _, p := range parts {
			if n := len(groups); n > 0 && h.pair && render.Width(groups[n-1])+2+render.Width(p) <= w {
				groups[n-1] += "  " + p
				continue
			}
			groups = append(groups, p)
		}
	}
	for i := 0; i < len(h.acts); i++ {
		a := bindingOf(h.s, h.acts[i])
		if a == nil {
			continue
		}
		ka := shownKeys(a)
		if h.pair && i+1 < len(h.acts) {
			if b := bindingOf(h.s, h.acts[i+1]); b != nil {
				i++
				kb := shownKeys(b)
				var zipped []string
				for j := 0; j < max(len(ka), len(kb)); j++ {
					switch {
					case j < len(ka) && j < len(kb):
						zipped = append(zipped, ka[j]+" / "+kb[j])
					case j < len(ka):
						zipped = append(zipped, ka[j])
					default:
						zipped = append(zipped, kb[j])
					}
				}
				pack(zipped)
				continue
			}
		}
		if one := strings.Join(ka, "  "); render.Width(one) <= w {
			groups = append(groups, one)
		} else {
			pack(ka)
		}
	}
	if !h.pair {
		if one := strings.Join(groups, " / "); render.Width(one) <= w {
			return []string{one}
		}
	}
	return groups
}

// imeNote: how every list action is reached when an IME eats lowercase letters, built from the table.
func imeNote() string {
	var alias, dialog, chips []string
	for i := range bindings {
		b := &bindings[i]
		if b.in&inList == 0 || !isLower(b.keys[0]) {
			continue
		}
		name := i18n.T(actName(b.act))
		switch {
		case b.ime == actViaDialog:
			dialog = append(dialog, name)
		case b.ime == actChips:
			chips = append(chips, name)
		case b.ime != actNone:
		default:
			for _, k := range b.keys[1:] {
				if _, named := keyNamesShown[k]; !named && !isLetter(k) && !isWide(k) {
					alias = append(alias, keyName(k)+" "+name)
					break
				}
			}
		}
	}
	sep := i18n.T("help.list_sep")
	return i18n.F("help.note_ime", strings.Join(alias, sep), strings.Join(dialog, sep), strings.Join(chips, sep), keyOf(inList, actChips))
}

// actName: the short name of an action (buttons, footer, the IME note).
func actName(a act) string {
	return map[act]string{
		actSort: "key.sort", actDown: "key.down", actUp: "key.up", actCopy: "key.copy", actTags: "key.tags",
		actProjects: "key.projects", actProvider: "key.provider", actDate: "key.date", actStatus: "key.status",
		actNew: "key.new", actFavorite: "key.favorite", actDone: "key.done", actArchive: "key.archive",
		actEdit: "key.edit", actQuit: "key.quit",
	}[a]
}

// trashBlocked: record actions do nothing on a deleted session; D there restores it.
func trashBlocked(a act) bool {
	b := bindingOf(inList, a)
	return b != nil && b.tier != tierNav && a != actDelete
}

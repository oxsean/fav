package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/render"
)

// paletteEntry is one action of the list as the help page lists it: its words, and the key that runs it; an action
// with no list key (a dialog's starting button) runs do instead.
type paletteEntry struct {
	desc string
	key  string
	do   func(*Model) tea.Cmd
}

// paletteEntries: every row of the help page that is not a pair of moves, one entry per action, keyed by the key the
// footer shows, then the dialogs' actions that have none; the palette itself is left out.
func paletteEntries() []paletteEntry {
	var out []paletteEntry
	for _, sec := range helpLayout() {
		for _, row := range sec.rows {
			if row.pair || row.s != inList {
				continue
			}
			for _, a := range row.acts {
				if b := bindingOf(inList, a); b != nil && a != actPalette {
					out = append(out, paletteEntry{desc: row.desc, key: b.keys[0]})
				}
			}
		}
	}
	return append(out, paletteEntry{desc: "resume.btn_make_task", do: func(m *Model) tea.Cmd { return m.makeTask(m.current()) }})
}

// openPalette lists the actions to pick one by its words in either language; picking it presses its key, or runs it.
func (m *Model) openPalette() {
	entries := paletteEntries()
	items := make([]item, len(entries))
	for i, e := range entries {
		items[i] = item{
			name:  strconv.Itoa(i) + " " + i18n.In(i18n.ZH, e.desc) + " " + i18n.In(i18n.EN, e.desc),
			label: render.Pad(keyName(e.key), 8) + i18n.T(e.desc),
		}
	}
	m.openPicker(i18n.T("palette.title"), i18n.T("palette.hint"), items, false, nil, func(mm *Model, chosen []string) {
		if len(chosen) == 0 {
			return
		}
		i, err := strconv.Atoi(strings.Fields(chosen[0])[0])
		if err != nil || i >= len(entries) {
			return
		}
		if do := entries[i].do; do != nil {
			mm.pending = tea.Batch(mm.pending, do(mm))
			return
		}
		mm.pending = tea.Batch(mm.pending, mm.navKey(keyMsg(entries[i].key)))
	})
}

var keyCodes = map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "up": tea.KeyUp, "down": tea.KeyDown,
	"left": tea.KeyLeft, "right": tea.KeyRight, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd}

// keyMsg is the key press whose String() is k, as the table spells keys.
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	}
	if c, ok := keyCodes[k]; ok {
		return tea.KeyPressMsg{Code: c}
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && utf8.RuneCountInString(rest) == 1 {
		r, _ := utf8.DecodeRuneInString(rest)
		return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
	}
	r, _ := utf8.DecodeRuneInString(k)
	return tea.KeyPressMsg{Code: r, Text: k}
}

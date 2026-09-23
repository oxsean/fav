package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestKeysAreUniquePerScope(t *testing.T) {
	for _, s := range scopes {
		seen := map[string]act{}
		for _, b := range bindings {
			if b.in&s == 0 {
				continue
			}
			for _, k := range b.keys {
				if a, dup := seen[k]; dup {
					t.Errorf("scope %d: %q is bound to %d and %d", s, k, a, b.act)
				}
				seen[k] = b.act
			}
		}
	}
}

func TestKeysAvoidReservedChords(t *testing.T) {
	for _, b := range bindings {
		for _, k := range b.keys {
			switch {
			case strings.HasPrefix(k, "alt+"):
				t.Errorf("%q: Alt belongs to Herdr and macOS Option", k)
			case k == "ctrl+a":
				t.Errorf("%q is Herdr's prefix", k)
			case k == "ctrl+i" || k == "ctrl+h" || k == "ctrl+m" || k == "ctrl+[":
				t.Errorf("%q is the same byte as Tab / Backspace / Enter / Esc", k)
			case k == "ctrl+c" && b.act != actQuit:
				t.Errorf("ctrl+c only quits")
			}
		}
	}
}

func TestStartingNeedsADialog(t *testing.T) {
	for _, b := range bindings {
		if b.in&inList != 0 && b.tier == tierStart {
			t.Errorf("act %d starts something straight from the list", b.act)
		}
	}
}

// Dialog rules: Tab only moves focus, Space only pages.
func TestDialogTabAndSpace(t *testing.T) {
	for _, s := range scopes[1:] {
		switch a := keyAct(s, "tab"); a {
		case actFocusNext, actTabNext:
		default:
			t.Errorf("scope %d: Tab does %d", s, a)
		}
		switch a := keyAct(s, "space"); a {
		case actNone, actPageDown:
		default:
			t.Errorf("scope %d: Space does %d", s, a)
		}
	}
}

func TestEveryListActionHasAnIMERoute(t *testing.T) {
	m := sized(t, 140, 40)
	m.askResume()
	m.screen()
	onButton := map[string]bool{}
	for _, b := range m.ov.btns {
		if k, _, ok := labelKey(b.label); ok {
			onButton[k] = true
		}
	}
	for _, b := range bindings {
		if b.in&inList == 0 || !isLower(b.keys[0]) || hasNonLetter(b.keys) {
			continue
		}
		switch {
		case b.ime == actNone:
			t.Errorf("%q has no route without letters", b.keys[0])
		case b.ime == actViaDialog:
			if !onButton[keyOf(inResume, b.act)] {
				t.Errorf("%q: no button in the resume dialog", b.keys[0])
			}
		default:
			if r := bindingOf(inList, b.ime); r == nil || !hasNonLetter(r.keys) {
				t.Errorf("%q: its route %d needs a non-letter key", b.keys[0], b.ime)
			}
		}
	}
	for _, b := range bindings {
		if b.in&inResume != 0 && b.tier == tierStart && b.act != actResume && b.act != actApp && !onButton[keyOf(inResume, b.act)] {
			t.Errorf("%q focuses a button that is not there", b.keys[0])
		}
	}
}

func hasNonLetter(keys []string) bool {
	for _, k := range keys {
		if !isLetter(k) {
			return true
		}
	}
	return false
}

func TestHelpListsEveryAction(t *testing.T) {
	type at struct {
		a act
		s scope
	}
	listed := map[at]bool{}
	for _, sec := range helpLayout() {
		for _, h := range sec.rows {
			for _, a := range h.acts {
				if bindingOf(h.s, a) == nil {
					t.Errorf("%s: act %d has no key in scope %d", h.desc, a, h.s)
				}
				listed[at{a, h.s}] = true
			}
		}
	}
	for _, b := range bindings { // dialogs draw their keys on their own buttons, so only the list's keys are required here
		if b.in&inList != 0 && !listed[at{b.act, inList}] {
			t.Errorf("%q is missing from the help page", b.keys[0])
		}
	}
}

// Keys on buttons and in the footer come from the table, so the texts must not carry one.
func TestLabelTextsCarryNoKeys(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		raw, err := os.ReadFile("../../i18n/locales/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var texts map[string]string
		if err := json.Unmarshal(raw, &texts); err != nil {
			t.Fatal(err)
		}
		for k, v := range texts {
			label := strings.HasPrefix(k, "footer.") || strings.HasPrefix(k, "key.") || strings.HasPrefix(k, "btn.") || strings.Contains(k, ".btn_")
			if _, _, ok := labelKey(v); label && ok {
				t.Errorf("%s %s = %q starts with a key", lang, k, v)
			}
		}
	}
}

package tui

import (
	"testing"

	"github.com/oxsean/fav/internal/i18n"
)

// The palette lists every action of the help page: each has a key the list takes, and its Chinese and English words
// both find it.
func TestThePaletteFindsEveryActionByEitherLanguage(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	entries := paletteEntries()
	if len(entries) < 30 {
		t.Fatalf("%d entries", len(entries))
	}
	for i, e := range entries {
		if got := keyMsg(e.key).String(); got != e.key {
			t.Errorf("%s: its key %q presses as %q", e.desc, e.key, got)
		}
		for _, lang := range []string{i18n.ZH, i18n.EN} {
			m := sized(t, 120, 40)
			m.openPalette()
			m.ov.filter.SetValue(i18n.In(lang, e.desc))
			found := false
			for _, it := range m.ov.visible() {
				found = found || it.name == m.ov.items[i].name
			}
			if !found {
				t.Errorf("%s: %q does not find it", e.desc, i18n.In(lang, e.desc))
			}
		}
	}
}

func TestThePaletteRunsWhatItPicks(t *testing.T) {
	t.Setenv("TEND_HOME", t.TempDir())
	m := sized(t, 120, 40)
	m.Update(press(":"))
	if m.ov.kind != ovPicker {
		t.Fatal(": opens the palette")
	}
	m.ov.filter.SetValue(i18n.In(i18n.EN, "help.settings"))
	m.Update(press("enter"))
	if m.ov.kind != ovSettings {
		t.Fatalf("picking settings opens them: %d", m.ov.kind)
	}
	m.Update(press("esc"))
	m.Update(press("："))
	m.ov.filter.SetValue(i18n.In(i18n.ZH, "help.help"))
	m.Update(press("enter"))
	if m.ov.kind != ovHelp {
		t.Fatalf("the full-width colon too, and the help page: %d", m.ov.kind)
	}
}

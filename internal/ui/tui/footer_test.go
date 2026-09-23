package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/oxsean/fav/internal/i18n"
)

func TestFooterColoursEveryKeyOfAHint(t *testing.T) {
	m := sized(t, 140, 40)
	m.Update(press(";"))
	if f := m.footer(); !strings.Contains(f, accent.Render(keyName("left")+" "+keyName("right"))) {
		t.Fatalf("both arrows of the filter-row hint are drawn as keys: %q", f)
	}
}

func TestFooterKeepsHelpAndSearch(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		i18n.Set(lang)
		for _, w := range []int{140, 100, 80, 60, 50} {
			m := sized(t, w, 30)
			f := ansi.Strip(m.footer())
			if ansi.StringWidth(f) > w || !strings.Contains(f, i18n.T("footer.help")) || !strings.Contains(f, i18n.T("footer.search")) {
				t.Errorf("%s %d: help and search stay, within the width: %q", lang, w, f)
			}
			if w >= 100 && strings.Contains(f, i18n.T("footer.delete")) {
				t.Errorf("%s %d: record management lives in the Enter dialog: %q", lang, w, f)
			}
		}
	}
	i18n.Set("zh")
}

func TestProjectHeaderFooterFollowsFolding(t *testing.T) {
	m := sized(t, 140, 40)
	m.view = viewProjects
	m.refresh()
	m.cursor = 0
	if m.current() != nil {
		t.Fatal("the projects view starts on a group header")
	}
	first := ansi.Strip(m.footer())
	m.toggleGroup()
	second := ansi.Strip(m.footer())
	has := func(f, k string) bool { return strings.Contains(f, i18n.T(k)) }
	if has(first, "footer.enter_expand") == has(second, "footer.enter_expand") || has(first, "footer.enter_collapse") == has(second, "footer.enter_collapse") {
		t.Fatalf("Enter says expand on a folded group and collapse on an open one: %q → %q", first, second)
	}
}

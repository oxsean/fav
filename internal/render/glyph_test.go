package render

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/oxsean/fav/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// runewidth and charmbracelet/x/ansi must agree on every icon's width, Nerd Font PUA included.
func TestGlyphWidthsAgreeAcrossImplementations(t *testing.T) {
	for name, set := range map[string][len(nerd)]string{"nerd": nerd, "ascii": ascii} {
		for i, g := range set {
			rw, aw := Width(g), ansi.StringWidth(g)
			if g == "" || rw != aw || rw < 1 || rw > 2 {
				t.Errorf("%s[%d] %q: runewidth %d, ansi %d", name, i, g, rw, aw)
			}
			line := g + " 排障 regression"
			if w, a := Width(Pad(line, 30)), ansi.StringWidth(Pad(line, 30)); w != 30 || a != 30 {
				t.Errorf("%s[%d]: Pad to 30 gives %d (runewidth) / %d (ansi)", name, i, w, a)
			}
		}
	}
}

package render

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// runewidth and charmbracelet/x/ansi must agree on the width of Nerd Font PUA characters.
func TestGlyphWidthsAgreeAcrossImplementations(t *testing.T) {
	glyphs := map[string]string{
		"Active": GlyphActive, "Done": GlyphDone, "Pinned": GlyphPinned,
		"Archive": GlyphArchive, "Arrow": GlyphArrow, "Open": GlyphOpen,
		"Closed": GlyphClosed, "OK": GlyphOK, "Warn": GlyphWarn, "Err": GlyphErr,
		"Search": GlyphSearch, "Project": GlyphProject, "Branch": GlyphBranch,
		"Dir": GlyphDir, "Tag": GlyphTag, "Clock": GlyphClock,
		"Term": GlyphTerm, "Herdr": GlyphHerdr, "Brand": GlyphBrand, "Live": GlyphLive,
	}
	for name, g := range glyphs {
		if g == "" {
			t.Errorf("Glyph%s 是空串", name)
			continue
		}
		rw, aw := Width(g), ansi.StringWidth(g)
		if rw != aw {
			t.Errorf("Glyph%s (%q): runewidth 算 %d 列，ansi 算 %d 列", name, g, rw, aw)
		}
		if rw != 1 && rw != 2 {
			t.Errorf("Glyph%s (%q) 宽度 %d 不合理", name, g, rw)
		}
	}
}

func TestPadWithGlyphs(t *testing.T) {
	for _, g := range []string{GlyphActive, GlyphDone, GlyphBranch, GlyphTag} {
		line := g + " 排障 regression"
		if got := Width(Pad(line, 30)); got != 30 {
			t.Errorf("Pad(%q, 30) 得到 %d 列", line, got)
		}
		if got := ansi.StringWidth(Pad(line, 30)); got != 30 {
			t.Errorf("ansi 视角下 Pad(%q, 30) 得到 %d 列", line, got)
		}
	}
}

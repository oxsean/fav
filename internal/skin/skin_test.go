package skin

import (
	"strings"
	"testing"
)

func check(t *testing.T, s Skin) {
	t.Helper()
	text := textRatio
	if s.Input.High {
		text = highRatio
	}
	for theme, p := range map[string]Palette{"light": s.Light, "dark": s.Dark} {
		for _, k := range Tokens {
			if p[k] == "" {
				t.Fatalf("%s %s: no %s", s.Name, theme, k)
			}
		}
		grounds := []string{"bg", "surface", "surface-alt", "surface-hover"}
		need := func(fg string, ratio float64, backs ...string) {
			t.Helper()
			for _, b := range backs {
				if c := Contrast(p[fg], p[b]); c < ratio {
					t.Errorf("%s %s: %s on %s is %.2f:1, under %.1f", s.Name, theme, fg, b, c, ratio)
				}
			}
		}
		for _, fg := range []string{"text", "muted", "danger", "warning", "success", "queued", "starting", "running", "unknown", "exited",
			"stopped", "failed", "canceled", "abandoned"} {
			need(fg, text, grounds...)
		}
		need("accent", text, append(grounds, "accent-soft")...)
		need("text", text, "accent-soft", "warning-soft", "danger-soft") // the TUI's selected rows, search hits and warnings
		need("faint", lineRatio, grounds...)
		need("surface", text, "accent", "danger") // a primary button's label
		need("danger", text, "danger-soft")
		need("warning", text, "warning-soft")
		need("focus", lineRatio, grounds...)
		if s.Input.High {
			need("border", lineRatio, grounds...)
		}
	}
}

func TestEveryPresetKeepsItsContrastInBothThemes(t *testing.T) {
	for _, p := range Presets {
		for _, high := range []bool{false, true} {
			in := p.Input
			in.High = high
			s, err := Make(p.Name, in)
			if err != nil {
				t.Fatal(err)
			}
			check(t, s)
		}
	}
}

func TestAnyAccentIsMovedUntilItReads(t *testing.T) {
	for _, accent := range []string{"#ffff00", "#000000", "#ffffff", "#00ffff", "#ff00ff", "#808080", "#0000ff"} {
		for _, base := range []string{"#5f6b7a", "#ff0000", "#000000", "#ffffff"} {
			for _, high := range []bool{false, true} {
				s, err := Make("custom", Input{Base: base, Accent: accent, High: high})
				if err != nil {
					t.Fatal(err)
				}
				check(t, s)
			}
		}
	}
}

func TestStatesKeepTheirColourWhateverTheAccent(t *testing.T) {
	a, _ := Named("tend")
	b, _ := Named("tend-c03030")
	for _, k := range []string{"running", "failed", "exited", "queued"} {
		if a.Light[k] != b.Light[k] || a.Dark[k] != b.Dark[k] {
			t.Fatalf("%s moved with the accent", k)
		}
	}
	if a.Light["accent"] == b.Light["accent"] {
		t.Fatal("the accent is the one asked for")
	}
}

func TestNames(t *testing.T) {
	for name, want := range map[string]Input{
		"tend":              Presets[0].Input,
		"forest-high":       {Base: Presets[1].Input.Base, Accent: Presets[1].Input.Accent, High: true},
		"ember-aa3300-high": {Base: Presets[2].Input.Base, Accent: "#aa3300", High: true},
	} {
		s, err := Named(name)
		if err != nil || s.Input != want {
			t.Fatalf("%s: %+v %v", name, s.Input, err)
		}
	}
	for _, bad := range []string{"", "nope", "tend-12345", "tend-GGGGGG", "tend-high-high", "../tend", "tend.css"} {
		if _, err := Named(bad); err == nil {
			t.Fatalf("%q", bad)
		}
	}
	s, _ := Named("graphite")
	css := s.CSS()
	for _, want := range []string{`:root { color-scheme: light;`, `:root[data-theme="dark"]`, `:root[data-theme="system"]`, "--accent: #"} {
		if !strings.Contains(css, want) {
			t.Fatalf("%s in\n%s", want, css)
		}
	}
}

func TestColoursSurviveTheRoundTrip(t *testing.T) {
	for _, hex := range []string{"#315fa5", "#ffffff", "#000000", "#b0502a", "#7f7f7f", "#00ff00"} {
		c, ok := toLCh(hex)
		if !ok || c.hex() != hex {
			t.Fatalf("%s → %+v → %s", hex, c, c.hex())
		}
	}
}

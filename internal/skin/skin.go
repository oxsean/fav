// Package skin derives a colour scheme's semantic tokens from a base colour (the neutrals' hue and tint), an accent and
// a contrast level, in CIE LCh, light and dark. The Web UI serves them as CSS; the TUI takes the same tokens.
package skin

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Input is what a skin is made from.
type Input struct {
	Base   string `json:"base"`   // #rrggbb
	Accent string `json:"accent"` // #rrggbb
	High   bool   `json:"high,omitempty"`
}

// Palette is a skin's tokens for one theme: name → #rrggbb, or a CSS value for overlay and shadow.
type Palette map[string]string

// Skin is a named skin in both themes.
type Skin struct {
	Name  string
	Input Input
	Light Palette
	Dark  Palette
}

// Preset is a built-in skin's name and input.
type Preset struct {
	Name  string `json:"name"`
	Input Input  `json:"input"`
}

// Presets are the built-in skins, the default first.
var Presets = []Preset{
	{"tend", Input{Base: "#5f6b7a", Accent: "#315fa5"}},
	{"forest", Input{Base: "#5b6b63", Accent: "#2d7a5b"}},
	{"ember", Input{Base: "#76685e", Accent: "#b0502a"}},
	{"graphite", Input{Base: "#6b6b6b", Accent: "#5a55c8"}},
}

// Tokens are every token a palette holds, in the order CSS lists them.
var Tokens = []string{"bg", "surface", "surface-alt", "surface-hover", "text", "muted", "faint", "border", "accent", "accent-soft", "focus",
	"danger", "danger-soft", "warning", "warning-soft", "success", "queued", "starting", "running", "unknown", "exited",
	"stopped", "failed", "canceled", "abandoned", "overlay", "shadow"}

// status is a state's own hue and chroma: states keep them whatever the accent, and differ in shape too.
type status struct{ H, C float64 }

var statuses = map[string]status{
	"danger": {25, 58}, "warning": {70, 52}, "success": {150, 42}, "queued": {70, 52}, "starting": {305, 48},
	"running": {265, 52}, "unknown": {55, 55}, "exited": {150, 42}, "failed": {25, 58}, "canceled": {320, 12},
	"abandoned": {350, 24},
}

// Ratios the tokens keep against the backgrounds they sit on (WCAG): text 4.5, graphics 3; high contrast raises text
// to 7 and holds borders to 3.
const (
	textRatio = 4.5
	highRatio = 7
	lineRatio = 3
)

// Make derives the skin named name from in.
func Make(name string, in Input) (Skin, error) {
	base, ok := toLCh(in.Base)
	if !ok {
		return Skin{}, fmt.Errorf("base %q: #rrggbb", in.Base)
	}
	acc, ok := toLCh(in.Accent)
	if !ok {
		return Skin{}, fmt.Errorf("accent %q: #rrggbb", in.Accent)
	}
	return Skin{Name: name, Input: in, Light: derive(base, acc, in.High, false), Dark: derive(base, acc, in.High, true)}, nil
}

func derive(base, acc lch, high, dark bool) Palette {
	tintC := min(base.C, 12)
	tint := func(L, scale float64) lch { return lch{L, tintC * scale, base.H} }
	text := textRatio
	if high {
		text = highRatio
	}
	p := Palette{}
	put := func(k string, c lch) lch { p[k] = c.hex(); return c }
	var fg, soft func(k string) lch
	if !dark {
		put("bg", tint(96.5, .25))
		put("surface", tint(100, 0))
		put("surface-alt", tint(98, .2))
		put("surface-hover", tint(94.5, .35))
		put("accent-soft", lch{95, min(acc.C, 14) * .6, acc.H})
		put("danger-soft", lch{96.5, 8, 25})
		put("warning-soft", lch{97, 10, 80})
		fg = func(k string) lch { s := statuses[k]; return lch{44, s.C, s.H} }
		p["overlay"] = tint(12, .6).hex() + "66"
		p["shadow"] = "0 14px 48px " + tint(12, .6).hex() + "22"
	} else {
		put("bg", tint(8, .3))
		put("surface", tint(12, .3))
		put("surface-alt", tint(10, .3))
		put("surface-hover", tint(18, .35))
		put("accent-soft", lch{22, min(acc.C, 20) * .8, acc.H})
		put("danger-soft", lch{18, 12, 25})
		put("warning-soft", lch{19, 12, 75})
		fg = func(k string) lch { s := statuses[k]; return lch{74, s.C * .7, s.H} }
		p["overlay"] = "#00000099"
		p["shadow"] = "0 14px 48px #00000055"
	}
	grounds := []string{p["bg"], p["surface"], p["surface-alt"], p["surface-hover"]}
	soft = func(k string) lch {
		switch k {
		case "danger", "failed":
			return ensure(fg(k), text, append(grounds, p["danger-soft"])...)
		case "warning", "queued", "unknown":
			return ensure(fg(k), text, append(grounds, p["warning-soft"])...)
		}
		return ensure(fg(k), text, grounds...)
	}
	for k := range statuses {
		put(k, soft(k))
	}
	if !dark {
		put("text", ensure(tint(17, .6), text, grounds...))
		put("muted", ensure(tint(44, .5), text, grounds...))
		put("faint", ensure(tint(60, .45), lineRatio, grounds...))
		put("border", tint(88.5, .35))
		put("accent", ensure(acc, text, append(grounds, p["accent-soft"])...))
		put("stopped", ensure(tint(45, .6), text, grounds...))
	} else {
		put("text", ensure(tint(90, .25), text, grounds...))
		put("muted", ensure(tint(68, .3), text, grounds...))
		put("faint", ensure(tint(50, .35), lineRatio, grounds...))
		put("border", tint(27, .35))
		put("accent", ensure(lch{74, min(acc.C, 45), acc.H}, text, append(grounds, p["accent-soft"])...))
		put("stopped", ensure(tint(72, .6), text, grounds...))
	}
	if high {
		put("border", ensure(func() lch { c, _ := toLCh(p["border"]); return c }(), lineRatio, grounds...))
	}
	a, _ := toLCh(p["accent"])
	put("focus", ensure(a, lineRatio, grounds...))
	return p
}

// css is palette p as custom properties.
func (p Palette) css() string {
	var b strings.Builder
	for _, k := range Tokens {
		fmt.Fprintf(&b, " --%s: %s;", k, p[k])
	}
	return b.String()
}

// CSS is the skin as a stylesheet: light on :root, dark for data-theme="dark", and for "system" when the system is dark.
func (s Skin) CSS() string {
	return fmt.Sprintf("/* tend skin %s, made by internal/skin */\n:root { color-scheme: light;%s }\n:root[data-theme=\"dark\"] { color-scheme: dark;%s }\n"+
		"@media (prefers-color-scheme: dark) { :root[data-theme=\"system\"] { color-scheme: dark;%s } }\n",
		s.Name, s.Light.css(), s.Dark.css(), s.Dark.css())
}

var nameRE = regexp.MustCompile(`^([a-z]+)(?:-([0-9a-f]{6}))?(-high)?$`)

// Named is the skin a name gives: a preset, optionally with its own accent and high contrast, as in "tend",
// "forest-high", "tend-2d7a5b" or "ember-aa3300-high".
func Named(name string) (Skin, error) {
	m := nameRE.FindStringSubmatch(name)
	if m == nil {
		return Skin{}, fmt.Errorf("skin %q", name)
	}
	i := slices.IndexFunc(Presets, func(p Preset) bool { return p.Name == m[1] })
	if i < 0 {
		return Skin{}, fmt.Errorf("skin %q", name)
	}
	in := Presets[i].Input
	if m[2] != "" {
		in.Accent = "#" + m[2]
	}
	in.High = m[3] != ""
	return Make(name, in)
}

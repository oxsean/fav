package skin

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// lch is a colour in CIE LCh(ab) under D65: lightness 0–100, chroma, hue in degrees.
type lch struct{ L, C, H float64 }

// ⚠️ CIE constants: ε = 216/24389, κ = 24389/27; D65 white point.
const (
	epsilon = 216.0 / 24389
	kappa   = 24389.0 / 27
	whiteX  = 0.95047
	whiteZ  = 1.08883
)

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v>>16) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, true
}

func linear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func gamma(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

func labF(t float64) float64 {
	if t > epsilon {
		return math.Cbrt(t)
	}
	return (kappa*t + 16) / 116
}

func toLCh(hex string) (lch, bool) {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return lch{}, false
	}
	r, g, b = linear(r), linear(g), linear(b)
	x := (0.4124564*r + 0.3575761*g + 0.1804375*b) / whiteX
	y := 0.2126729*r + 0.7151522*g + 0.0721750*b
	z := (0.0193339*r + 0.1191920*g + 0.9503041*b) / whiteZ
	fx, fy, fz := labF(x), labF(y), labF(z)
	L, A, B := 116*fy-16, 500*(fx-fy), 200*(fy-fz)
	h := math.Atan2(B, A) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return lch{L, math.Hypot(A, B), h}, true
}

// rgb is c in linear sRGB, possibly outside the gamut.
func (c lch) rgb() (r, g, b float64) {
	a, bb := c.C*math.Cos(c.H*math.Pi/180), c.C*math.Sin(c.H*math.Pi/180)
	fy := (c.L + 16) / 116
	fx, fz := fy+a/500, fy-bb/200
	inv := func(f float64) float64 {
		if f*f*f > epsilon {
			return f * f * f
		}
		return (116*f - 16) / kappa
	}
	y := c.L / kappa
	if c.L > kappa*epsilon {
		y = fy * fy * fy
	}
	x, z := inv(fx)*whiteX, inv(fz)*whiteZ
	return 3.2404542*x - 1.5371385*y - 0.4985314*z, -0.9692660*x + 1.8760108*y + 0.0415560*z, 0.0556434*x - 0.2040259*y + 1.0572252*z
}

func inGamut(r, g, b float64) bool {
	const e = 1e-6
	return r >= -e && r <= 1+e && g >= -e && g <= 1+e && b >= -e && b <= 1+e
}

// hex is c in sRGB; a colour outside the gamut keeps its lightness and hue and loses chroma until it fits.
func (c lch) hex() string {
	c.L = min(max(c.L, 0), 100)
	if r, g, b := c.rgb(); !inGamut(r, g, b) {
		lo, hi := 0.0, c.C
		for range 30 {
			c.C = (lo + hi) / 2
			if r, g, b := c.rgb(); inGamut(r, g, b) {
				lo = c.C
			} else {
				hi = c.C
			}
		}
		c.C = lo
	}
	r, g, b := c.rgb()
	ch := func(v float64) int { return int(math.Round(min(max(gamma(min(max(v, 0), 1)), 0), 1) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", ch(r), ch(g), ch(b))
}

// luminance is WCAG's relative luminance of hex.
func luminance(hex string) float64 {
	r, g, b, _ := parseHex(hex)
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// Contrast is WCAG's contrast ratio of two #rrggbb colours, 1–21.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// ensure moves c's lightness away from the backgrounds until it has ratio against each of them.
func ensure(c lch, ratio float64, backs ...string) lch {
	dark := true // on light backgrounds a colour gets darker
	for _, b := range backs {
		dark = dark && luminance(b) > 0.18
	}
	step := -0.5
	if !dark {
		step = 0.5
	}
	c.L = min(max(c.L, 0), 100)
	for c.L >= 0 && c.L <= 100 {
		ok := true
		for _, b := range backs {
			ok = ok && Contrast(c.hex(), b) >= ratio
		}
		if ok {
			break
		}
		c.L += step
	}
	return c
}

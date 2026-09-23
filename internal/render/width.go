// Package render draws rows, cards and previews shared by fzf and the TUI. ⚠️ All width math goes through go-runewidth, never len().
package render

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func init() {
	// East Asian Ambiguous pinned narrow
	runewidth.DefaultCondition.EastAsianWidth = false
}

func Width(s string) int { return runewidth.StringWidth(s) }

func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}

// CutPad cuts or pads to exactly width cells, no ellipsis.
func CutPad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	n := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if n+rw > width {
			s = s[:i]
			break
		}
		n += rw
	}
	if n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func Pad(s string, width int) string {
	s = Truncate(s, width)
	if n := width - runewidth.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func PadLeft(s string, width int) string {
	s = Truncate(s, width)
	if n := width - runewidth.StringWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// Wrap breaks by display width: CJK per character, Latin on whitespace, over-wide words per character.
func Wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		for _, t := range glue(tokenize(para, width), width) {
			sep := ""
			if t.space && cur != "" {
				sep = " "
			}
			if cur != "" && Width(cur)+Width(sep)+Width(t.text) > width {
				lines = append(lines, cur)
				cur, sep = "", ""
			}
			cur += sep + t.text
		}
		lines = append(lines, cur)
	}
	return lines
}

// ⚠️ Line-breaking rules (kinsoku): these never start a line, and opening ones never end one.
const (
	noLineStart = "，。、；：！？）」』》〉】…—,.;:!?)]}>"
	noLineEnd   = "（「『《〈【“‘([{<"
)

// glue sticks a closing mark to the token before it and an opening mark to the token after it, while the pair still fits
// the width; only where the source had no space between them.
func glue(ts []tok, width int) []tok {
	first := func(s string) string {
		for _, r := range s {
			return string(r)
		}
		return ""
	}
	last := func(s string) string {
		_, n := utf8.DecodeLastRuneInString(s)
		return s[len(s)-n:]
	}
	out := make([]tok, 0, len(ts))
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		for strings.Contains(noLineEnd, last(t.text)) && i+1 < len(ts) && !ts[i+1].space && Width(t.text+ts[i+1].text) <= width {
			i++
			t.text += ts[i].text
		}
		if n := len(out); n > 0 && !t.space && strings.Contains(noLineStart, first(t.text)) && Width(out[n-1].text+t.text) <= width {
			out[n-1].text += t.text
			continue
		}
		out = append(out, t)
	}
	return out
}

// space remembers whether the source had a space here
type tok struct {
	text  string
	space bool
}

func tokenize(s string, width int) []tok {
	var out []tok
	space, ascii := false, ""
	flushASCII := func() {
		for Width(ascii) > width {
			cut, n := "", 0
			for _, r := range ascii {
				rw := runewidth.RuneWidth(r)
				if n+rw > width {
					break
				}
				cut += string(r)
				n += rw
			}
			if cut == "" {
				break
			}
			out = append(out, tok{cut, space})
			ascii, space = ascii[len(cut):], false
		}
		if ascii != "" {
			out = append(out, tok{ascii, space})
			ascii, space = "", false
		}
	}

	for _, r := range s {
		switch {
		case r == ' ' || r == '\t':
			flushASCII()
			space = true
		case runewidth.RuneWidth(r) > 1:
			flushASCII()
			out = append(out, tok{string(r), space})
			space = false
		default:
			ascii += string(r)
		}
	}
	flushASCII()
	return out
}

// foreground and bold only, no backgrounds

type style string

// 256 colours matching the TUI theme. ⚠️ Not lipgloss: the preview runs in an fzf child without a TTY and lipgloss strips colours there.
const (
	reset  = "\x1b[0m"
	dim    = style("\x1b[38;5;110m")
	bold   = style("\x1b[1m")
	cyan   = style("\x1b[38;5;81m")
	green  = style("\x1b[38;5;114m")
	yellow = style("\x1b[38;5;214m")
	red    = style("\x1b[38;5;210m")
	blue   = style("\x1b[38;5;117m")
)

var noColor = os.Getenv("NO_COLOR") != ""

func (s style) p(text string) string {
	if noColor || text == "" {
		return text
	}
	return string(s) + text + reset
}

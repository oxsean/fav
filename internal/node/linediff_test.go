package node

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("l" + strconv.Itoa(i) + "\n")
	}
	return b.String()
}

// apply applies unified hunks to a, as patch would; ok false when a hunk does not fit.
func apply(a string, hunks []hunk) (string, bool) {
	old := strings.SplitAfter(a, "\n")
	if old[len(old)-1] == "" {
		old = old[:len(old)-1]
	}
	var out []string
	at := 0
	for _, h := range hunks {
		var start, n int
		if _, err := fmtSscanHunk(h.At, &start, &n); err != nil {
			return "", false
		}
		if n > 0 {
			start--
		}
		for at < start {
			out = append(out, old[at])
			at++
		}
		for i, l := range h.Lines {
			if l == noNewline {
				continue
			}
			bare := i+1 < len(h.Lines) && h.Lines[i+1] == noNewline
			body := l[1:]
			if !bare {
				body += "\n"
			}
			switch l[0] {
			case ' ', '-':
				if at >= len(old) || old[at] != body {
					return "", false
				}
				at++
				if l[0] == ' ' {
					out = append(out, body)
				}
			case '+':
				out = append(out, body)
			}
		}
	}
	out = append(out, old[at:]...)
	return strings.Join(out, ""), true
}

func fmtSscanHunk(at string, start, n *int) (int, error) {
	// "@@ -s,n +s,n @@" or "@@ -s +s @@"
	old := strings.Fields(at)[1][1:]
	s, c, found := strings.Cut(old, ",")
	var err error
	if *start, err = strconv.Atoi(s); err != nil {
		return 0, err
	}
	*n = 1
	if found {
		*n, err = strconv.Atoi(c)
	}
	return 2, err
}

func TestLineDiffShapes(t *testing.T) {
	a := numbered(20)
	cases := []struct {
		name, a, b string
		ats        []string
		lines      [][]string
	}{
		{"same", a, a, nil, nil},
		{"one line", a, strings.Replace(a, "l10\n", "L10\n", 1), []string{"@@ -7,7 +7,7 @@"},
			[][]string{{" l7", " l8", " l9", "-l10", "+L10", " l11", " l12", " l13"}}},
		{"two apart", a, strings.Replace(strings.Replace(a, "l2\n", "L2\n", 1), "l19\n", "L19\n", 1), []string{"@@ -1,5 +1,5 @@", "@@ -16,5 +16,5 @@"}, nil},
		{"two near merge", a, strings.Replace(strings.Replace(a, "l8\n", "L8\n", 1), "l13\n", "L13\n", 1), []string{"@@ -5,12 +5,12 @@"}, nil},
		{"new file", "", "x\ny\n", []string{"@@ -0,0 +1,2 @@"}, [][]string{{"+x", "+y"}}},
		{"emptied", "x\ny\n", "", []string{"@@ -1,2 +0,0 @@"}, [][]string{{"-x", "-y"}}},
		{"no newline", "a\nb", "a\nb\n", []string{"@@ -1,2 +1,2 @@"}, [][]string{{" a", "-b", noNewline, "+b"}}},
		{"single", "a\n", "b\n", []string{"@@ -1 +1 @@"}, [][]string{{"-a", "+b"}}},
	}
	for _, c := range cases {
		hs := lineDiff(c.a, c.b, 3)
		if len(hs) != len(c.ats) {
			t.Fatalf("%s: %d hunks %+v", c.name, len(hs), hs)
		}
		for i, h := range hs {
			if h.At != c.ats[i] {
				t.Errorf("%s: hunk %d at %q", c.name, i, h.At)
			}
			if c.lines != nil && strings.Join(h.Lines, "|") != strings.Join(c.lines[i], "|") {
				t.Errorf("%s: hunk %d lines %q", c.name, i, h.Lines)
			}
		}
		if got, ok := apply(c.a, hs); !ok || got != c.b {
			t.Errorf("%s: applied %q ok %v", c.name, got, ok)
		}
	}
}

// Whatever the two texts, the hunks turn the first into the second, with any context; past the edit budget too.
func TestLineDiffApplies(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	text := func() string {
		var b strings.Builder
		for i, n := 0, r.Intn(60); i < n; i++ {
			b.WriteString(string(rune('a'+r.Intn(5))) + "\n")
		}
		if r.Intn(4) == 0 {
			b.WriteString("tail")
		}
		return b.String()
	}
	for i := 0; i < 2000; i++ {
		a, b := text(), text()
		ctx := r.Intn(5)
		if got, ok := apply(a, lineDiff(a, b, ctx)); !ok || got != b {
			t.Fatalf("%q → %q (context %d): %q ok %v", a, b, ctx, got, ok)
		}
	}
	defer func(m int) { maxDiffEdits = m }(maxDiffEdits)
	maxDiffEdits = 3
	for i := 0; i < 500; i++ {
		a, b := text(), text()
		if got, ok := apply(a, lineDiff(a, b, 2)); !ok || got != b {
			t.Fatalf("over budget %q → %q: %q ok %v", a, b, got, ok)
		}
	}
}

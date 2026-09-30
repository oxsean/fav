package node

import (
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		hs := lineDiff(c.a, c.b, 3, false)
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
		if got, ok := apply(a, lineDiff(a, b, ctx, false)); !ok || got != b {
			t.Fatalf("%q → %q (context %d): %q ok %v", a, b, ctx, got, ok)
		}
	}
	defer func(m int) { maxDiffEdits = m }(maxDiffEdits)
	maxDiffEdits = 3
	for i := 0; i < 500; i++ {
		a, b := text(), text()
		if got, ok := apply(a, lineDiff(a, b, 2, false)); !ok || got != b {
			t.Fatalf("over budget %q → %q: %q ok %v", a, b, got, ok)
		}
	}
}

var funcName = regexp.MustCompile(`^(@@ [^@]* @@).*$`)

// gitNoIndex is the hunks git diff --no-index gives from a to b, each "at\nline\n…", without the text git puts after
// a hunk's @@.
func gitNoIndex(t *testing.T, a, b string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte(a), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), []byte(b), 0o644)
	cmd := exec.Command("git", append(append([]string{"-c", "core.autocrlf=false", "diff", "--no-index", "--no-color"}, args...), "a", "b")...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); err != nil && (!ok || ee.ExitCode() != 1) {
		t.Fatalf("git diff: %v", err)
	}
	return joinHunks(parseHunks(string(out)))
}

func joinHunks(hs []hunk) string {
	var b strings.Builder
	for _, h := range hs {
		b.WriteString(funcName.ReplaceAllString(h.At, "$1") + "\n")
		for _, l := range h.Lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// Ignoring whitespace is git diff -w: lines that differ only in spaces, tabs, carriage returns and the last newline
// are the same, and are shown as the new side has them; other characters, however blank, count.
func TestLineDiffIgnoringSpaceIsGits(t *testing.T) {
	needGit(t)
	cases := [][2]string{
		{"a\nX\n c\nd\ne\n", "a\nY\nc \nd\ne\n"},
		{"a\nb", "a\nb\n"},
		{"a\nb\n", "a\r\nb\n"},
		{"a\nb\nc\n", "a\nb\nc\nd"},
		{"a\nb", "a\nb\nc\n"},
		{"a\nb\nc\n", "a\nb"},
		{"q\na\nb\n", "Q\na\nb"},
		{"a b\n", "ab\n"},
		{"a\n\nb\n", "a\n  \nb\n"},
		{"a\n", "a\v\n"},
		{"a\n", "a\f\n"},
		{"a\n", "a\u00a0\n"},
		{"", "x\n"},
		{"x\n", ""},
		{"func f() {\n\treturn 1\n}\n", "func f() {\n    return 2\n}\n"},
		{numbered(30), strings.NewReplacer("l3\n", "  l3\n", "l15\n", "L15\n", "l27\n", "\tl27\t\n").Replace(numbered(30))},
	}
	for _, c := range cases {
		want := gitNoIndex(t, c[0], c[1], "-w", "-U3")
		if got := joinHunks(lineDiff(c[0], c[1], 3, true)); got != want {
			t.Errorf("%q → %q:\n%s\ngit:\n%s", c[0], c[1], got, want)
		}
	}
}

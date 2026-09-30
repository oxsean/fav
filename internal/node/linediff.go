package node

import (
	"slices"
	"strconv"
	"strings"
)

// hunk is one place of a unified diff: its "@@ -a,b +c,d @@" line and its lines, each ' ', '-' or '+' and the text
// (noNewline after a last line without one).
type hunk struct {
	At    string   `json:"at"`
	Lines []string `json:"lines"`
}

const noNewline = `\ No newline at end of file`

// maxDiffEdits is how many lines lineDiff looks for the fewest edits within; past it the rest reads as removed and
// added whole. A variable for the tests.
var maxDiffEdits = 2000

type diffOp struct {
	kind   byte // ' ' '-' '+'
	ai, bi int  // the line of a (' ', '-') and of b (' ', '+')
}

// lineDiff is the unified diff of a to b with context lines around each change (Myers). ignoreSpace compares lines as
// git diff -w does, and takes the lines both keep from b, as git shows them.
func lineDiff(a, b string, context int, ignoreSpace bool) []hunk {
	al, bl := splitLines(a), splitLines(b)
	ids := map[string]int{}
	intern := func(ls []string) []int {
		out := make([]int, len(ls))
		for i, l := range ls {
			if ignoreSpace {
				l = withoutSpace(l)
			}
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	A, B := intern(al), intern(bl)
	pre := 0
	for pre < len(A) && pre < len(B) && A[pre] == B[pre] {
		pre++
	}
	suf := 0
	for suf < len(A)-pre && suf < len(B)-pre && A[len(A)-1-suf] == B[len(B)-1-suf] {
		suf++
	}
	var ops []diffOp
	for i := 0; i < pre; i++ {
		ops = append(ops, diffOp{' ', i, i})
	}
	for _, o := range myers(A[pre:len(A)-suf], B[pre:len(B)-suf]) {
		ops = append(ops, diffOp{o.kind, o.ai + pre, o.bi + pre})
	}
	for i := suf; i > 0; i-- {
		ops = append(ops, diffOp{' ', len(A) - i, len(B) - i})
	}
	return hunksOf(ops, al, bl, context, ignoreSpace)
}

// withoutSpace is l without what git diff -w ignores: ⚠️ git's isspace, space, tab, CR and LF only.
func withoutSpace(l string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, l)
}

// splitLines is s's lines, each with its newline (a last one may lack it).
func splitLines(s string) []string {
	ls := strings.SplitAfter(s, "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// myers is the fewest removals and additions turning a into b, with the lines both keep; past maxDiffEdits, all of a
// removed and all of b added.
func myers(a, b []int) []diffOp {
	n, m := len(a), len(b)
	whole := func() []diffOp {
		ops := make([]diffOp, 0, n+m)
		for i := range a {
			ops = append(ops, diffOp{'-', i, 0})
		}
		for j := range b {
			ops = append(ops, diffOp{'+', n, j})
		}
		return ops
	}
	if n == 0 || m == 0 {
		return whole()
	}
	max := n + m
	off := max + 1
	v := make([]int, 2*max+3)
	var trace [][]int
	for d := 0; d <= max; d++ {
		if d > maxDiffEdits {
			return whole()
		}
		trace = append(trace, slices.Clone(v[off-d:off+d+1]))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m, d)
			}
		}
	}
	return whole()
}

func backtrack(trace [][]int, x, y, d int) []diffOp {
	var rev []diffOp
	for ; d > 0; d-- {
		prev := trace[d] // v before step d, k from -d at index 0
		at := func(k int) int { return prev[k+d] }
		k := x - y
		var pk int
		if k == -d || k != d && at(k-1) < at(k+1) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			x, y = x-1, y-1
			rev = append(rev, diffOp{' ', x, y})
		}
		if x == px {
			y--
			rev = append(rev, diffOp{'+', x, y})
		} else {
			x--
			rev = append(rev, diffOp{'-', x, y})
		}
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, diffOp{' ', x, y})
	}
	slices.Reverse(rev)
	// removals before additions within each run of changes, as diff prints them
	for i := 0; i < len(rev); {
		if rev[i].kind == ' ' {
			i++
			continue
		}
		j := i
		for j < len(rev) && rev[j].kind != ' ' {
			j++
		}
		slices.SortStableFunc(rev[i:j], func(p, q diffOp) int {
			if p.kind == q.kind {
				return 0
			}
			if p.kind == '-' {
				return -1
			}
			return 1
		})
		i = j
	}
	return rev
}

// hunksOf groups ops into hunks with context lines of what both keep around each change; changes closer than twice
// that share a hunk.
func hunksOf(ops []diffOp, a, b []string, context int, keptFromB bool) []hunk {
	cover := make([]int, len(ops)+1)
	for i, o := range ops {
		if o.kind != ' ' {
			cover[max(0, i-context)]++
			cover[min(len(ops), i+context+1)]--
		}
	}
	var out []hunk
	depth := 0
	start := -1
	aPos, bPos := 0, 0 // lines of a and b before op i
	var sa, sb int
	for i := 0; i <= len(ops); i++ {
		depth += cover[i]
		if i < len(ops) && depth > 0 && start < 0 {
			start, sa, sb = i, aPos, bPos
		}
		if start >= 0 && (i == len(ops) || depth == 0) {
			out = append(out, makeHunk(ops[start:i], a, b, sa, sb, keptFromB))
			start = -1
		}
		if i < len(ops) {
			if ops[i].kind != '+' {
				aPos++
			}
			if ops[i].kind != '-' {
				bPos++
			}
		}
	}
	return out
}

func makeHunk(ops []diffOp, a, b []string, sa, sb int, keptFromB bool) hunk {
	var lines []string
	na, nb := 0, 0
	for _, o := range ops {
		text := ""
		switch o.kind {
		case ' ':
			text, na, nb = a[o.ai], na+1, nb+1
			if keptFromB {
				text = b[o.bi]
			}
		case '-':
			text, na = a[o.ai], na+1
		case '+':
			text, nb = b[o.bi], nb+1
		}
		body, nl := strings.CutSuffix(text, "\n")
		lines = append(lines, string(o.kind)+body)
		if !nl {
			lines = append(lines, noNewline)
		}
	}
	return hunk{At: "@@ -" + span(sa, na) + " +" + span(sb, nb) + " @@", Lines: lines}
}

// span is a hunk's side: its first line and count, the line before it when it is empty, ",1" left out.
func span(before, n int) string {
	switch n {
	case 0:
		return strconv.Itoa(before) + ",0"
	case 1:
		return strconv.Itoa(before + 1)
	}
	return strconv.Itoa(before+1) + "," + strconv.Itoa(n)
}

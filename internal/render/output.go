package render

import (
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/output"
)

// OutputLine is a line of a run's output in spans of one tone each. Edit is the key of an edited file's row that opens
// on its first hunk, the same across readings of the same events; empty on every other line.
type OutputLine struct {
	Spans []Span
	Edit  string
}

type Span struct {
	Text string
	Tone string
}

// Tones of a span: lines added, removed, a hunk's header, something said about what is left out; "" is plain.
const (
	ToneAdd  = "add"
	ToneDel  = "del"
	ToneHunk = "hunk"
	ToneDim  = "dim"
)

// PreviewRows is how many of a first hunk's lines an open row shows.
const PreviewRows = 8

func (l OutputLine) Text() string {
	var b strings.Builder
	for _, sp := range l.Spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

func plain(s string) OutputLine { return OutputLine{Spans: []Span{{Text: s}}} }

// RunOutputLines are a run's events as lines to read. Prefixes: "> " the user, "+ " a tool call, "$ " a command,
// "~ " changed files, "? " a question or approval, "- " a warning, an answer or lost output, "= " the result, "! " an error or what a failed
// call ended with; the agent's words as they are. Thoughts, system lines and a call's result that did not fail are
// left out. Of what is still being written, a thought is one line saying so, a command still running its last lines.
// An edit step with the node's counts (edits: a codex step's own, a claude call's on its result) is a row per file,
// exactly width wide, +a and −d toned; a row open says (by its Edit key) is followed by its first hunk, each line at
// most width wide.
func RunOutputLines(evs []output.Event, width int, open func(key string) bool) []OutputLine {
	var out []OutputLine
	results := map[string][]output.Edit{}
	for _, e := range evs {
		if e.Kind == output.KindToolResult && e.Ref != "" && len(e.Edits) > 0 {
			results[e.Ref] = e.Edits
		}
	}
	files := func(e output.Event, edits []output.Edit) {
		for i, x := range edits {
			l := editRow(x, width)
			if len(x.Preview) > 0 {
				l.Edit = strconv.FormatInt(e.Off, 10) + ":" + strconv.Itoa(i) + ":" + x.Path
			}
			out = append(out, l)
			if l.Edit != "" && open != nil && open(l.Edit) {
				out = append(out, firstHunk(x, width)...)
			}
		}
	}
	add := func(prefix, text string) {
		for l := range strings.Lines(text) {
			if l = strings.TrimRight(l, "\r\n"); strings.TrimSpace(l) != "" {
				out = append(out, plain(prefix+l))
				prefix = ""
			}
		}
	}
	for _, e := range evs {
		switch e.Kind {
		case output.KindUser, output.KindYou:
			add("> ", e.Text)
		case output.KindResolved:
			add("- ", i18n.T("tasks.output_resolved"))
		case output.KindInterrupt:
			add("! ", i18n.T("tasks.output_interrupted"))
		case output.KindGap:
			add("- ", i18n.T("tasks.output_gap"))
		case output.KindSay, output.KindRaw:
			add("", e.Text)
		case output.KindThink:
			if e.Temp {
				add("- ", i18n.T("tasks.output_thinking"))
			}
		case output.KindCmd:
			if e.Temp {
				prefix, title := callLine(e)
				add(prefix, title+" "+i18n.T("tasks.output_running"))
				for _, l := range tailLines(e.Output, output.RunningTail) {
					add("  ", l)
				}
				continue
			}
			add(callLine(e))
			if e.Error {
				add("! ", lastLine(e.Output))
			}
		case output.KindTool, output.KindEdit, output.KindMCP:
			switch edits := results[e.Call]; {
			case e.Kind == output.KindEdit && len(e.Edits) > 0:
				files(e, e.Edits)
			case e.Family == output.FamilyEdit && e.Call != "" && len(edits) > 0:
				files(e, edits)
			default:
				add(callLine(e))
			}
			if e.Error && e.Kind != output.KindTool {
				add("! ", lastLine(e.Output))
			}
		case output.KindToolResult:
			if e.Error {
				add("! ", lastLine(e.Output))
			}
		case output.KindSys:
			if e.Level == "warning" {
				add("- ", e.Text)
			}
		case output.KindError:
			add("! ", e.Text)
		case output.KindResult:
			if e.Error {
				add("! ", e.Text)
			} else {
				add("= ", e.Text)
			}
		}
	}
	return out
}

var editGlyphs = map[string]string{"add": "+", "modify": "~", "delete": "−", "rename": "→"}

// editRow is one edited file exactly width wide: its glyph and path on the left, the path's start cut when it does not
// fit, and on the right +a −d with its hunks, or a new file's lines.
func editRow(x output.Edit, width int) OutputLine {
	glyph := editGlyphs[x.Op]
	if glyph == "" {
		glyph = "~"
	}
	name := x.Path
	if x.Op == "rename" && x.From != "" {
		name = x.From + " → " + x.Path
	}
	stat := []Span{{Text: "+" + strconv.Itoa(x.Add), Tone: ToneAdd}, {Text: " "}, {Text: "−" + strconv.Itoa(x.Del), Tone: ToneDel}}
	switch {
	case x.Op == "add":
		stat = []Span{{Text: i18n.F("tasks.output_new_file", x.Add)}}
	case x.Hunks > 1:
		stat = append(stat, Span{Text: " · " + i18n.F("tasks.output_hunks", x.Hunks)})
	}
	statW := Width(OutputLine{Spans: stat}.Text())
	room := width - Width(glyph) - 1 - 2 - statW
	if room < 4 {
		return plain(Pad(glyph+" "+name+"  "+OutputLine{Spans: stat}.Text(), width))
	}
	return OutputLine{Spans: append([]Span{{Text: glyph + " " + Pad(cutStart(name, room), room) + "  "}}, stat...)}
}

// firstHunk is x's first hunk under its row: PreviewRows of its lines at most, toned by their marks and cut to width,
// then what is left out of it and how many hunks follow.
func firstHunk(x output.Edit, width int) []OutputLine {
	const indent = "  "
	var out []OutputLine
	for _, l := range x.Preview[:min(len(x.Preview), PreviewRows)] {
		l = strings.ReplaceAll(Sanitize(l), "\t", "    ")
		tone := ""
		switch {
		case strings.HasPrefix(l, "@@"):
			tone = ToneHunk
		case strings.HasPrefix(l, "+"):
			tone = ToneAdd
		case strings.HasPrefix(l, "-"):
			tone = ToneDel
		}
		out = append(out, OutputLine{Spans: []Span{{Text: indent}, {Text: Truncate(l, width-len(indent)), Tone: tone}}})
	}
	note := func(s string) {
		out = append(out, OutputLine{Spans: []Span{{Text: indent}, {Text: Truncate(s, width-len(indent)), Tone: ToneDim}}})
	}
	switch {
	case len(x.Preview) > PreviewRows:
		note(i18n.F("tasks.output_more_lines", len(x.Preview)-PreviewRows))
	case x.Cut:
		note(i18n.T("tasks.output_hunk_cut"))
	}
	if x.Hunks > 1 {
		note(i18n.F("tasks.output_more_hunks", x.Hunks-1))
	}
	return out
}

// cutStart keeps the end of s within width, an ellipsis in place of what it drops.
func cutStart(s string, width int) string {
	if Width(s) <= width {
		return s
	}
	rs := []rune(s)
	w, i := 1, len(rs)
	for i > 0 && w+Width(string(rs[i-1])) <= width {
		i--
		w += Width(string(rs[i]))
	}
	return "…" + string(rs[i:])
}

// callLine is a call's prefix and title, with what the title leaves out and a command's exit code.
func callLine(e output.Event) (string, string) {
	title := e.Title
	if n := e.More; n > 0 {
		switch e.Family {
		case output.FamilyShell:
			title += " " + i18n.F("tasks.output_more_lines", n)
		case output.FamilyEdit:
			title += " " + i18n.F("tasks.output_more_files", n)
		case output.FamilyAsk:
			title += " " + i18n.F("tasks.output_more_questions", n)
		}
	}
	if e.Exit != nil {
		title += " (exit " + strconv.Itoa(*e.Exit) + ")"
	}
	switch {
	case e.Family == output.FamilyAsk:
		return "? ", title
	case e.Family == output.FamilyShell:
		return "$ ", title
	case e.Family == output.FamilyEdit:
		return "~ ", title
	case e.Family == output.FamilyMCP || e.Tool == "":
		return "+ ", title
	}
	return "+ ", strings.TrimSpace(e.Tool + " " + title)
}

func tailLines(s string, n int) []string {
	if s = strings.TrimRight(s, "\r\n"); s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	return lines[max(0, len(lines)-n):]
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	return lines[len(lines)-1]
}

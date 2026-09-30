package render

import (
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/output"
)

// RunOutputLines are a run's events as lines to read. Prefixes: "> " the user, "+ " a tool call, "$ " a command,
// "~ " changed files, "? " a question or approval, "- " a warning, an answer or lost output, "= " the result, "! " an error or what a failed
// call ended with; the agent's words as they are. Thoughts, system lines and a call's result that did not fail are
// left out. Of what is still being written, a thought is one line saying so, a command still running its last lines.
// An edit step with the node's counts (edits: a codex step's own, a claude call's on its result) is a row per file,
// exactly width wide.
func RunOutputLines(evs []output.Event, width int) []string {
	var out []string
	results := map[string][]output.Edit{}
	for _, e := range evs {
		if e.Kind == output.KindToolResult && e.Ref != "" && len(e.Edits) > 0 {
			results[e.Ref] = e.Edits
		}
	}
	files := func(edits []output.Edit) {
		for _, x := range edits {
			out = append(out, editRow(x, width))
		}
	}
	add := func(prefix, text string) {
		for l := range strings.Lines(text) {
			if l = strings.TrimRight(l, "\r\n"); strings.TrimSpace(l) != "" {
				out = append(out, prefix+l)
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
				files(e.Edits)
			case e.Family == output.FamilyEdit && e.Call != "" && len(edits) > 0:
				files(edits)
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
func editRow(x output.Edit, width int) string {
	glyph := editGlyphs[x.Op]
	if glyph == "" {
		glyph = "~"
	}
	name := x.Path
	if x.Op == "rename" && x.From != "" {
		name = x.From + " → " + x.Path
	}
	stat := "+" + strconv.Itoa(x.Add) + " −" + strconv.Itoa(x.Del)
	switch {
	case x.Op == "add":
		stat = i18n.F("tasks.output_new_file", x.Add)
	case x.Hunks > 1:
		stat += " · " + i18n.F("tasks.output_hunks", x.Hunks)
	}
	room := width - Width(glyph) - 1 - 2 - Width(stat)
	if room < 4 {
		return Pad(glyph+" "+name+"  "+stat, width)
	}
	return glyph + " " + Pad(cutStart(name, room), room) + "  " + stat
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

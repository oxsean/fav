package render

import (
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/output"
)

// RunOutputLines are a run's events as lines to read. Prefixes: "> " the user, "+ " a tool call, "$ " a command,
// "~ " changed files, "? " a question or approval, "- " a warning, "= " the result, "! " an error or what a failed
// call ended with; the agent's words as they are. Thoughts, system lines and a call's result that did not fail are
// left out.
func RunOutputLines(evs []output.Event) []string {
	var out []string
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
		case output.KindUser:
			add("> ", e.Text)
		case output.KindSay, output.KindRaw:
			add("", e.Text)
		case output.KindTool, output.KindCmd, output.KindEdit, output.KindMCP:
			add(callLine(e))
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

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	return lines[len(lines)-1]
}

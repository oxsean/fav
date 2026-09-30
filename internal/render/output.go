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

package output

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/shell"
)

// Families maps a tool's name, as claude and codex call it, to its family; a name missing here is FamilyOther, and
// claude's MCP tools (mcp__server__tool) are FamilyMCP.
var Families = map[string]string{
	"Bash": FamilyShell, "BashOutput": FamilyShell, "KillShell": FamilyShell, "KillBash": FamilyShell,
	"commandExecution": FamilyShell, "command_execution": FamilyShell,
	"Read": FamilyRead, "NotebookRead": FamilyRead,
	"Grep": FamilySearch, "Glob": FamilySearch, "LS": FamilySearch,
	"Edit": FamilyEdit, "MultiEdit": FamilyEdit, "Write": FamilyEdit, "NotebookEdit": FamilyEdit,
	"fileChange": FamilyEdit, "file_change": FamilyEdit,
	"WebFetch": FamilyWeb, "WebSearch": FamilyWeb, "webSearch": FamilyWeb, "web_search": FamilyWeb,
	"Task": FamilyAgent, "Agent": FamilyAgent,
	"TodoWrite": FamilyPlan, "update_plan": FamilyPlan, "todo_list": FamilyPlan,
	"AskUserQuestion": FamilyAsk,
	"mcpToolCall":     FamilyMCP, "mcp_tool_call": FamilyMCP,
}

// FamilyOf is the family of the tool called name.
func FamilyOf(name string) string {
	if f, ok := Families[name]; ok {
		return f
	}
	if strings.HasPrefix(name, "mcp__") {
		return FamilyMCP
	}
	return FamilyOther
}

// maxTitle is the most runes a title keeps.
const maxTitle = 200

// titleOf is a call's one-line summary and how many more of what it summarizes it leaves out: a command's further
// lines, an edit's further files, a question's further questions.
func titleOf(family, name string, input json.RawMessage) (string, int) {
	var in struct {
		Command   string `json:"command"`
		FilePath  string `json:"file_path"`
		Notebook  string `json:"notebook_path"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
		Pattern   string `json:"pattern"`
		Path      string `json:"path"`
		Glob      string `json:"glob"`
		OldString Text   `json:"old_string"`
		NewString Text   `json:"new_string"`
		Content   Text   `json:"content"`
		NewSource Text   `json:"new_source"`
		Edits     []struct {
			OldString Text `json:"old_string"`
			NewString Text `json:"new_string"`
		} `json:"edits"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
		Subagent    string `json:"subagent_type"`
		Questions   []struct {
			Question string `json:"question"`
		} `json:"questions"`
		Todos json.RawMessage `json:"todos"`
	}
	json.Unmarshal(input, &in)
	switch family {
	case FamilyShell:
		if in.Command == "" {
			return name, 0
		}
		return shellTitle(in.Command)
	case FamilyRead:
		p := firstOf(in.FilePath, in.Notebook)
		switch {
		case in.Offset > 0 && in.Limit > 0:
			p += ":" + strconv.Itoa(in.Offset) + "-" + strconv.Itoa(in.Offset+in.Limit-1)
		case in.Offset > 0:
			p += ":" + strconv.Itoa(in.Offset) + "-"
		case in.Limit > 0:
			p += ":1-" + strconv.Itoa(in.Limit)
		}
		return oneLine(p), 0
	case FamilySearch:
		where := firstOf(in.Path, in.Glob)
		if in.Pattern == "" {
			return oneLine(where), 0
		}
		if where == "" {
			return oneLine(in.Pattern), 0
		}
		return oneLine(in.Pattern + " · " + where), 0
	case FamilyEdit:
		add, del := 0, 0
		switch {
		case len(in.Edits) > 0:
			for _, e := range in.Edits {
				add, del = add+e.NewString.lines(), del+e.OldString.lines()
			}
		case !in.Content.empty() || name == "Write":
			add = in.Content.lines()
		case !in.NewSource.empty():
			add = in.NewSource.lines()
		default:
			add, del = in.NewString.lines(), in.OldString.lines()
		}
		return oneLine(firstOf(in.FilePath, in.Notebook)) + " " + counts(add, del), 0
	case FamilyWeb:
		if in.URL != "" {
			if u, err := url.Parse(in.URL); err == nil && u.Host != "" {
				return oneLine(u.Host + u.EscapedPath()), 0
			}
			return oneLine(in.URL), 0
		}
		return oneLine(in.Query), 0
	case FamilyAgent:
		return oneLine(firstOf(in.Description, in.Subagent)), 0
	case FamilyPlan:
		return planTitle(in.Todos, "content", "status"), 0
	case FamilyAsk:
		if len(in.Questions) > 0 {
			return oneLine(in.Questions[0].Question), len(in.Questions) - 1
		}
		return "", 0
	case FamilyMCP:
		server, tool := "", name
		if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
			server, tool, _ = strings.Cut(rest, "__")
		}
		return mcpTitle(server, tool, input), 0
	}
	return oneLine(firstString(input)), 0
}

// shellTitle is a command's first line, what a shell -c wraps unwrapped, and the count of lines after it.
func shellTitle(command string) (string, int) {
	if words, ok := shell.POSIX.Split(command); ok && len(words) == 3 &&
		slices.Contains([]string{"sh", "bash", "zsh"}, filepath.Base(words[0])) && (words[1] == "-c" || words[1] == "-lc") {
		command = words[2]
	}
	command = strings.TrimSpace(command)
	first, rest, _ := strings.Cut(command, "\n")
	more := 0
	if rest != "" {
		more = strings.Count(rest, "\n") + 1
	}
	return clip(strings.TrimRight(first, "\r")), more
}

func mcpTitle(server, tool string, args json.RawMessage) string {
	t := tool
	if server != "" {
		t = server + "." + tool
	}
	if a := firstString(args); a != "" {
		t += " " + a
	}
	return oneLine(t)
}

// planTitle is a plan's steps done of all, "3/7"; a step is done when its status is "completed" or its done key is
// true.
func planTitle(raw json.RawMessage, textKey, doneKey string) string {
	var steps []map[string]any
	if json.Unmarshal(raw, &steps) != nil || len(steps) == 0 {
		var wrapped struct {
			Plan []map[string]any `json:"plan"`
		}
		if json.Unmarshal(raw, &wrapped) != nil || len(wrapped.Plan) == 0 {
			return ""
		}
		steps = wrapped.Plan
	}
	done := 0
	for _, s := range steps {
		if v := s[doneKey]; v == "completed" || v == true {
			done++
		}
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(len(steps))
}

// firstString is the first string value at the top of a JSON object, in the order written.
func firstString(raw json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for d.More() {
		if _, err := d.Token(); err != nil {
			return ""
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ownReport tells a call of the run's own tend run note|ask|verdict|plan, which the node allows without asking.
func ownReport(tool string, input json.RawMessage) bool {
	if tool != "Bash" {
		return false
	}
	var in struct {
		Command string `json:"command"`
	}
	return json.Unmarshal(input, &in) == nil && agent.OwnReport(in.Command, "")
}

func counts(add, del int) string { return "+" + strconv.Itoa(add) + " −" + strconv.Itoa(del) }

func diffCounts(diff string) (add, del int) {
	for l := range strings.Lines(diff) {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			add++
		case strings.HasPrefix(l, "-"):
			del++
		}
	}
	return add, del
}

func oneLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return clip(strings.TrimRight(first, "\r"))
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxTitle {
		return s
	}
	return string([]rune(s)[:maxTitle-1]) + "…"
}

// Output keeps a tool's first and last keepLines lines, each end at most keepBytes.
const (
	keepLines = 40
	keepBytes = 16 << 10
)

// shown is output as an event carries it: headTail's, or all of it for a whole line.
func shown(s string, whole bool) (out string, lines, size int, truncated map[string]int) {
	if whole {
		return s, Lines(s), len(s), nil
	}
	return headTail(s)
}

// headTail is output as an event carries it: whole when short, else its first and last lines joined by a line "…",
// with the lines and bytes it had and the bytes left out.
func headTail(s string) (out string, lines, size int, truncated map[string]int) {
	if s == "" {
		return "", 0, 0, nil
	}
	lines, size = Lines(s), len(s)
	head, tail := s, ""
	if all := strings.SplitAfter(strings.TrimSuffix(s, "\n"), "\n"); len(all) > 2*keepLines {
		head, tail = strings.Join(all[:keepLines], ""), strings.Join(all[len(all)-keepLines:], "")
		if strings.HasSuffix(s, "\n") {
			tail += "\n"
		}
	}
	head = cutBytes(head, keepBytes, false)
	if tail == "" {
		tail = cutBytes(s[len(head):], keepBytes, true)
	} else {
		tail = cutBytes(tail, keepBytes, true)
	}
	left := size - len(head) - len(tail)
	if left <= 0 {
		return s, lines, size, nil
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return head + "…\n" + tail, lines, size, map[string]int{"output": left}
}

// cutBytes keeps at most n bytes of s from its start, or from its end, on rune boundaries.
func cutBytes(s string, n int, end bool) string {
	if len(s) <= n {
		return s
	}
	if end {
		i := len(s) - n
		for i < len(s) && !utf8.RuneStart(s[i]) {
			i++
		}
		return s[i:]
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

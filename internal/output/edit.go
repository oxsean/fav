package output

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ⚠️ An edit step's previews (the first hunk of each file) share these, so a step on the live stream stays small.
const (
	PreviewLines = 40
	PreviewBytes = 8 << 10
)

// Edit is one file an edit step changed: op add | modify | delete | rename (From the old path), the lines added and
// removed and the hunks. The node counts them before slimming and writes them on the line (its "tend" field), with
// Preview, the first hunk's "@@" line and lines (Cut: some left out); Lines is every hunk, read only whole.
type Edit struct {
	Path    string   `json:"path"`
	Op      string   `json:"op"`
	From    string   `json:"from,omitempty"`
	Add     int      `json:"add"`
	Del     int      `json:"del"`
	Hunks   int      `json:"hunks"`
	Preview []string `json:"preview,omitempty"`
	Cut     bool     `json:"cut,omitempty"`
	Lines   []string `json:"lines,omitempty"`
}

// Stat is what the node writes on a line whose step edits files.
type Stat struct {
	Edits []Edit `json:"edits,omitempty"`
}

// ClaudeEdits reads the file a claude tool result (tool_use_result) changed: its structuredPatch, or the content of a
// file it created. nil for a result that is no edit.
func ClaudeEdits(res json.RawMessage) []Edit {
	var r struct {
		Type     string          `json:"type"`
		FilePath string          `json:"filePath"`
		Content  Text            `json:"content"`
		Patch    json.RawMessage `json:"structuredPatch"`
	}
	if json.Unmarshal(res, &r) != nil || r.FilePath == "" {
		return nil
	}
	if r.Type == "create" {
		if r.Content.Ref != nil {
			return []Edit{{Path: r.FilePath, Op: "add", Add: r.Content.Ref.Lines, Hunks: 1}}
		}
		return []Edit{whole(r.FilePath, "add", r.Content.S)}
	}
	var hunks []struct {
		OldStart int      `json:"oldStart"`
		OldLines int      `json:"oldLines"`
		NewStart int      `json:"newStart"`
		NewLines int      `json:"newLines"`
		Lines    []string `json:"lines"`
	}
	patch := r.Patch
	var text string
	if json.Unmarshal(patch, &text) == nil {
		patch = json.RawMessage(text) // put back from a blob: the array's JSON
	}
	if len(patch) == 0 || json.Unmarshal(patch, &hunks) != nil {
		return nil
	}
	e := Edit{Path: r.FilePath, Op: "modify", Hunks: len(hunks)}
	for _, h := range hunks {
		e.Lines = append(e.Lines, "@@ -"+strconv.Itoa(h.OldStart)+","+strconv.Itoa(h.OldLines)+" +"+strconv.Itoa(h.NewStart)+","+strconv.Itoa(h.NewLines)+" @@")
		e.Lines = append(e.Lines, h.Lines...)
	}
	e.Add, e.Del = marks(e.Lines)
	return []Edit{e}
}

// CodexEdits reads the files of a codex fileChange item (its changes): an update's unified diff, an added or deleted
// file's content as it is, a move (move_path) a rename.
func CodexEdits(changes json.RawMessage) []Edit {
	var cs []struct {
		Path string          `json:"path"`
		Kind json.RawMessage `json:"kind"`
		Diff Text            `json:"diff"`
	}
	if json.Unmarshal(changes, &cs) != nil {
		return nil
	}
	var out []Edit
	for _, c := range cs {
		var kind struct {
			Type     string `json:"type"`
			MovePath string `json:"move_path"`
		}
		if json.Unmarshal(c.Kind, &kind.Type) != nil {
			json.Unmarshal(c.Kind, &kind)
		}
		var e Edit
		switch {
		case c.Diff.Ref != nil:
			e = Edit{Path: c.Path, Op: "modify"}
			if kind.Type == "add" {
				e.Op, e.Add = "add", c.Diff.Ref.Lines
			}
		case kind.Type == "add":
			e = whole(c.Path, "add", c.Diff.S)
		case kind.Type == "delete":
			e = whole(c.Path, "delete", c.Diff.S)
		default:
			e = Edit{Path: c.Path, Op: "modify", Lines: splitDiff(c.Diff.S)}
			for _, l := range e.Lines {
				if strings.HasPrefix(l, "@@") {
					e.Hunks++
				}
			}
			e.Add, e.Del = marks(e.Lines)
		}
		if kind.MovePath != "" && kind.MovePath != c.Path {
			e.Op, e.From, e.Path = "rename", c.Path, kind.MovePath
		}
		out = append(out, e)
	}
	return out
}

// whole is a file added or deleted whole: its content is the one hunk.
func whole(path, op, content string) Edit {
	ls := splitDiff(content)
	e := Edit{Path: path, Op: op}
	if len(ls) == 0 {
		return e
	}
	mark, head := "+", "@@ -0,0 +1,"+strconv.Itoa(len(ls))+" @@"
	if op == "delete" {
		mark, head = "-", "@@ -1,"+strconv.Itoa(len(ls))+" +0,0 @@"
	}
	e.Lines = append(e.Lines, head)
	for _, l := range ls {
		e.Lines = append(e.Lines, mark+l)
	}
	e.Hunks = 1
	if op == "delete" {
		e.Del = len(ls)
	} else {
		e.Add = len(ls)
	}
	return e
}

func splitDiff(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// marks counts a diff's added and removed lines, its file headers left out.
func marks(lines []string) (add, del int) {
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			add++
		case strings.HasPrefix(l, "-"):
			del++
		}
	}
	return add, del
}

// Brief is edits as a step on the live stream carries them: each file's first hunk as its preview while the step's
// PreviewBytes last, at most PreviewLines of it, a line too long for what is left cut short; Lines left out.
func Brief(edits []Edit) []Edit {
	left := PreviewBytes
	out := make([]Edit, len(edits))
	for i, e := range edits {
		first := e.Lines
		for j := 1; j < len(first); j++ {
			if strings.HasPrefix(first[j], "@@") {
				first = first[:j]
				break
			}
		}
		e.Lines, e.Preview = nil, nil
		for _, l := range first {
			if len(e.Preview) == PreviewLines || left < 2 {
				e.Cut = true
				break
			}
			if len(l)+1 > left {
				l = clipBytes(l, left-len("…")-1) + "…"
				e.Cut = true
			}
			e.Preview = append(e.Preview, l)
			left -= len(l) + 1
		}
		out[i] = e
	}
	return out
}

// clipBytes is s cut to at most n bytes, on a rune's boundary.
func clipBytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// editTitle is an edit step's title: the first file and the lines added and removed over all of them.
func editTitle(edits []Edit) (string, int) {
	if len(edits) == 0 {
		return "", 0
	}
	add, del := 0, 0
	for _, e := range edits {
		add, del = add+e.Add, del+e.Del
	}
	return edits[0].Path + " " + counts(add, del), len(edits) - 1
}

package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/i18n"
)

// Claude line: {"type":"user","message":{"content":"text"}}, an array content is a tool_result;
// Codex line: {"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":…}]}}.
type transcriptLine struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	Cwd        string    `json:"cwd"`
	Entrypoint string    `json:"entrypoint"`       // Claude: cli = a human, sdk-cli = -p / SDK
	IsMeta     bool      `json:"isMeta"`           // Claude: injected content, not typed
	IsSummary  bool      `json:"isCompactSummary"` // Claude: the recap written when the context ran out
	Message    struct {
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"` // Claude assistant lines: what this request sent, i.e. the context
	} `json:"message"`
	Payload struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Name      string          `json:"name"`      // Codex function_call
		Arguments string          `json:"arguments"` // Codex function_call: JSON string
		Output    json.RawMessage `json:"output"`    // Codex function_call_output / custom_tool_call_output: string or block array
		Input     string          `json:"input"`     // Codex custom_tool_call (apply_patch): free text
		Info      *struct {
			Last struct {
				Input int `json:"input_tokens"`
			} `json:"last_token_usage"`
			Window int `json:"model_context_window"`
		} `json:"info"` // Codex token_count
	} `json:"payload"`
}

// Step keeps truncated args and output only; the full text is re-read at Off.
type Step struct {
	Tool   string
	Text   string
	Files  []string // what an edit tool or apply_patch wrote
	Result bool
	Off    int64
	Idx    int
}

const (
	stepArgLines    = 12 // max lines kept of a command / file body
	stepResultLines = 8
	stepCharCap     = 1200 // max chars kept per line
)

// steps: Claude has tool_use blocks on assistant lines and tool_result on user lines, Codex function_call / custom_tool_call and
// their outputs; full skips truncation.
func (l *transcriptLine) steps(off int64, full bool) []Step {
	argLines, resLines := stepArgLines, stepResultLines
	if full {
		argLines, resLines = 1<<30, 1<<30
	}
	var out []Step
	switch {
	case (l.Type == "user" || l.Type == "assistant") && len(l.Message.Content) > 0 && l.Message.Content[0] == '[':
		var blocks []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(l.Message.Content, &blocks)
		for i, b := range blocks {
			switch b.Type {
			case "tool_use":
				st := Step{Tool: b.Name, Text: toolArg(b.Name, b.Input, argLines), Off: off, Idx: i}
				if p := EditedPath(b.Name, b.Input); p != "" {
					st.Files = []string{p}
				}
				out = append(out, st)
			case "tool_result":
				out = append(out, Step{Result: true, Text: head(ContentText(b.Content), resLines), Off: off, Idx: i})
			}
		}
	case l.Type == "response_item" && l.Payload.Type == "function_call":
		out = append(out, Step{Tool: l.Payload.Name, Text: toolArg(l.Payload.Name, json.RawMessage(l.Payload.Arguments), argLines), Off: off})
	case l.Type == "response_item" && l.Payload.Type == "custom_tool_call":
		st := Step{Tool: l.Payload.Name, Text: head(l.Payload.Input, argLines), Off: off}
		if l.Payload.Name == "apply_patch" {
			if st.Files = PatchFiles(l.Payload.Input); len(st.Files) > 0 {
				st.Text = strings.Join(st.Files, " ") + "\n" + head(l.Payload.Input, max(1, argLines-1))
			}
		}
		out = append(out, st)
	case l.Type == "response_item" && (l.Payload.Type == "function_call_output" || l.Payload.Type == "custom_tool_call_output"):
		out = append(out, Step{Result: true, Text: head(ContentText(l.Payload.Output), resLines), Off: off})
	}
	return out
}

// patchFileMarks are apply_patch's file headers ("*** Move to:" names the new path of an update).
var patchFileMarks = []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "}

// PatchFiles are the files an apply_patch input touches, as written (relative paths stay relative).
func PatchFiles(input string) []string {
	var files []string
	for l := range strings.SplitSeq(input, "\n") {
		for _, m := range patchFileMarks {
			if p, ok := strings.CutPrefix(l, m); ok {
				files = append(files, strings.TrimSpace(p))
			}
		}
	}
	return files
}

// EditedPath is the file an edit tool call writes (Edit, Write, MultiEdit, NotebookEdit); "" for any other tool.
func EditedPath(tool string, input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	switch tool {
	case "Edit", "Write", "MultiEdit":
		json.Unmarshal(input, &in)
		return in.FilePath
	case "NotebookEdit":
		json.Unmarshal(input, &in)
		return in.NotebookPath
	}
	return ""
}

const (
	msgTextCap = 16 * 1024 // max bytes of one message kept in memory
	truncMark  = " …"
)

func lineAt(f *os.File, br *bufio.Reader, off int64) (*transcriptLine, bool) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, false
	}
	br.Reset(f)
	b, err := br.ReadBytes('\n')
	if err != nil && len(b) == 0 {
		return nil, false
	}
	var l transcriptLine
	if json.Unmarshal(b, &l) != nil {
		return nil, false
	}
	return &l, true
}

func readLineAt(path string, off int64) (*transcriptLine, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	return lineAt(f, bufio.NewReaderSize(f, 1<<20), off)
}

// TextFull re-reads the full text at Message.Off (memory keeps msgTextCap); falls back when unreadable.
func TextFull(path string, off int64, fallback string) string {
	if l, ok := readLineAt(path, off); ok {
		if m := l.speech(); m.Text != "" {
			return m.Text
		}
	}
	return fallback
}

func Truncated(text string) bool { return len(text) > msgTextCap && strings.HasSuffix(text, truncMark) }

func StepsFull(path string, steps []Step) []string {
	out := make([]string, len(steps))
	for i, st := range steps {
		out[i] = st.Text
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	byOff := map[int64][]Step{}
	for _, st := range steps {
		byOff[st.Off] = append(byOff[st.Off], st)
	}
	for off := range byOff {
		l, ok := lineAt(f, br, off)
		if !ok {
			continue
		}
		full := l.steps(off, true)
		for i, st := range steps {
			if st.Off != off {
				continue
			}
			for _, s := range full {
				if s.Idx == st.Idx && s.Result == st.Result {
					out[i] = s.Text
				}
			}
		}
	}
	return out
}

// toolArg: Bash → the command (multi-line kept), Write → path + head of the file, Edit → path + one line before and after,
// anything else → the first string argument or flattened JSON.
func toolArg(name string, raw json.RawMessage, n int) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil && json.Unmarshal([]byte(s), &in) != nil {
			return head(s, n)
		}
	}
	str := func(k string) string { v, _ := in[k].(string); return v }
	switch name {
	case "Write":
		return str("file_path") + "\n" + head(str("content"), n-1)
	case "Edit":
		if n > stepArgLines {
			return str("file_path") + i18n.T("diff.before") + str("old_string") + i18n.T("diff.after") + str("new_string")
		}
		return str("file_path") + "\n- " + firstLine(str("old_string")) + "\n+ " + firstLine(str("new_string"))
	}
	for _, k := range []string{"command", "cmd", "file_path", "path", "pattern", "query", "url", "description", "prompt"} {
		if v := str(k); v != "" {
			return head(v, n)
		}
	}
	b, _ := json.MarshalIndent(in, "", "  ")
	return head(string(b), n)
}

func head(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	rest := len(lines) - n
	if rest > 0 {
		lines = lines[:n]
	}
	if n <= stepArgLines {
		for i, l := range lines {
			lines[i] = clip(l, stepCharCap)
		}
	}
	if rest > 0 {
		lines = append(lines, i18n.F("transcript.more_lines", rest))
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimLeft(s, "\n"), "\n")
	return clip(l, stepCharCap)
}

// ContentText: message, tool result or Codex tool output content, a string or blocks whose texts join as paragraphs.
func ContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func clip(s string, n int) string {
	s = strings.TrimRight(s, " \t")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}

// Unwrap strips the <pasted_content id="…">…</pasted_content> wrapper Claude Code adds.
func Unwrap(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<pasted_content") {
		return s
	}
	if i := strings.IndexByte(s, '>'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "</pasted_content"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// injected: prefixes of user-message text Claude Code writes itself (tags, recaps, images, skills, hooks, interrupts).
var injected = []string{"<", "This session is being continued", "[Image:", "Base directory for this skill",
	"Launching skill", "Stop hook feedback", "A session-scoped Stop hook", "[Request interrupted"}

// Injected: user-message text the agent wrote, not the user.
func Injected(s string) bool {
	for _, n := range injected {
		if strings.HasPrefix(s, n) {
			return true
		}
	}
	return false
}

// prompt is what the user typed on this line; "" for anything else.
func (l *transcriptLine) prompt() string {
	var s string
	switch {
	case l.Type == "user" && !l.IsMeta:
		s = ContentText(l.Message.Content)
	case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "user":
		s = ContentText(l.Payload.Content)
	}
	if s = Unwrap(s); Injected(s) {
		return ""
	}
	return s
}

type Message struct {
	Role  string
	Text  string
	Chars int // rune count before truncation
	Off   int64
	At    time.Time
	Steps []Step // what the AI did after this message and before the next, offsets only
}

func (l *transcriptLine) speech() Message {
	role, text := l.rawSpeech()
	text = strings.Join(strings.Fields(text), " ")
	if role == "" || text == "" || l.IsMeta || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "[") {
		return Message{}
	}
	return Message{Role: role, Text: text, Chars: utf8.RuneCountInString(text), At: l.Timestamp.Local()}
}

// rawSpeech is the line's prose with its line breaks.
func (l *transcriptLine) rawSpeech() (role, text string) {
	switch {
	case l.Type == "user" || l.Type == "assistant":
		role, text = l.Type, ContentText(l.Message.Content)
	case l.Type == "response_item" && l.Payload.Type == "message":
		role, text = l.Payload.Role, ContentText(l.Payload.Content)
	}
	return role, strings.TrimSpace(Unwrap(text))
}

func rawText(path string, off int64, fallback string) string {
	if l, ok := readLineAt(path, off); ok {
		if _, t := l.rawSpeech(); t != "" {
			return t
		}
	}
	return fallback
}

// Page is newest first; From is the offset of the oldest line, Done means the file head was reached.
type Page struct {
	Msgs []Message
	From int64
	Done bool
}

// Messages reads chunks backwards from before (< 0 = file end) until n messages or the head;
// lines longer than a chunk are skipped, tool steps attach to the preceding message.
func Messages(path string, before int64, n int) Page {
	const chunk = 1024 * 1024
	f, err := os.Open(path)
	if err != nil {
		return Page{Done: true}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Page{Done: true}
	}
	if before < 0 || before > st.Size() {
		before = st.Size()
	}
	page := Page{From: before}
	var pending []Step // tool steps newer than the current message, attached once their message is read
	drained := true
	for len(page.Msgs) < n && before > 0 {
		start := max(before-chunk, 0)
		buf, err := io.ReadAll(io.NewSectionReader(f, start, before-start))
		if err != nil {
			break
		}
		lineStart := start
		if start > 0 { // the partial first line belongs to the next chunk
			nl := bytes.IndexByte(buf, '\n')
			if nl < 0 || start+int64(nl)+1 >= before {
				before = start
				continue
			}
			buf, lineStart = buf[nl+1:], start+int64(nl)+1
		}
		before = lineStart
		if len(buf) > 0 && buf[len(buf)-1] == '\n' {
			buf = buf[:len(buf)-1]
		}
		lines := bytes.Split(buf, []byte{'\n'})
		offs := make([]int64, len(lines))
		off := lineStart
		for i, l := range lines {
			offs[i] = off
			off += int64(len(l)) + 1
		}
		drained = true
		for i, b := range slices.Backward(lines) {
			if len(page.Msgs) >= n {
				drained = false
				break
			}

			if len(b) == 0 || !interesting(b) {
				continue
			}
			var l transcriptLine
			if json.Unmarshal(b, &l) != nil {
				continue
			}
			if st := l.steps(offs[i], false); len(st) > 0 {
				pending = append(st, pending...)
			}
			if m := l.speech(); m.Text != "" {
				if len(m.Text) > msgTextCap {
					m.Text = capText(m.Text, msgTextCap) + truncMark
				}
				m.Off, m.Steps, pending = offs[i], pending, nil
				page.Msgs = append(page.Msgs, m)
				page.From = offs[i]
			}
		}
	}
	page.Done = before == 0 && drained
	if page.Done {
		page.From = 0
	}
	return page
}

func interesting(b []byte) bool {
	return bytes.Contains(b, []byte(`"type":"user"`)) || bytes.Contains(b, []byte(`"type":"assistant"`)) ||
		bytes.Contains(b, []byte(`"type":"message"`)) || bytes.Contains(b, []byte(`"type":"function_call`)) ||
		bytes.Contains(b, []byte(`"type":"custom_tool_call`))
}

func recentMessages(path string, n int) []Message { return Messages(path, -1, n).Msgs }

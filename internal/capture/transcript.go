package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"github.com/oxsean/fav/internal/i18n"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Claude line: {"type":"user","message":{"content":"text"}}, an array content is a tool_result;
// Codex line: {"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":…}]}}.
type transcriptLine struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	Cwd        string    `json:"cwd"`
	Entrypoint string    `json:"entrypoint"` // Claude: cli = a human, sdk-cli = -p / SDK
	IsMeta     bool      `json:"isMeta"`     // Claude: injected content, not typed
	Message    struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Name      string          `json:"name"`      // Codex function_call
		Arguments string          `json:"arguments"` // Codex function_call: JSON string
		Output    json.RawMessage `json:"output"`    // Codex function_call_output: string or block array
	} `json:"payload"`
}

// Step keeps truncated args and output only; the full text is re-read at Off.
type Step struct {
	Tool   string
	Text   string
	Result bool
	Off    int64
	Idx    int
}

const (
	stepArgLines    = 12 // max lines kept of a command / file body
	stepResultLines = 8
	stepCharCap     = 1200 // max chars kept per line
)

// steps: Claude has tool_use blocks on assistant lines and tool_result on user lines, Codex function_call / function_call_output; full skips truncation.
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
				out = append(out, Step{Tool: b.Name, Text: toolArg(b.Name, b.Input, argLines), Off: off, Idx: i})
			case "tool_result":
				out = append(out, Step{Result: true, Text: head(blockText(b.Content), resLines), Off: off, Idx: i})
			}
		}
	case l.Type == "response_item" && l.Payload.Type == "function_call":
		out = append(out, Step{Tool: l.Payload.Name, Text: toolArg(l.Payload.Name, json.RawMessage(l.Payload.Arguments), argLines), Off: off})
	case l.Type == "response_item" && l.Payload.Type == "function_call_output":
		out = append(out, Step{Result: true, Text: head(blockText(l.Payload.Output), resLines), Off: off})
	}
	return out
}

const (
	msgTextCap = 16 * 1024 // max bytes of one message kept in memory
	truncMark  = " …"
)

// TextFull re-reads the full text at Message.Off (memory keeps msgTextCap); falls back when unreadable.
func TextFull(path string, off int64, fallback string) string {
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()
	if _, err := f.Seek(off, 0); err != nil {
		return fallback
	}
	b, err := bufio.NewReaderSize(f, 1<<20).ReadBytes('\n')
	if err != nil && len(b) == 0 {
		return fallback
	}
	var l transcriptLine
	if json.Unmarshal(b, &l) != nil {
		return fallback
	}
	if m := l.speech(); m.Text != "" {
		return m.Text
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
		if _, err := f.Seek(off, 0); err != nil {
			continue
		}
		br.Reset(f)
		b, err := br.ReadBytes('\n')
		if err != nil && len(b) == 0 {
			continue
		}
		var l transcriptLine
		if json.Unmarshal(b, &l) != nil {
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

// tool_result content is a string or [{type:text,text}]
func blockText(raw json.RawMessage) string {
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
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
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

func (l *transcriptLine) prompt() string {
	var s string
	switch {
	case l.Type == "user" && len(l.Message.Content) > 0 && l.Message.Content[0] == '"':
		json.Unmarshal(l.Message.Content, &s)
	case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "user" && len(l.Payload.Content) > 0:
		s = l.Payload.Content[0].Text
	}
	s = Unwrap(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

type Prompt struct {
	Text string
	At   time.Time
}

// only the last 512KB of the file
func LastPrompt(path string) Prompt {
	const tail = 512 * 1024
	f, err := os.Open(path)
	if err != nil {
		return Prompt{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Prompt{}
	}
	off := st.Size() - tail
	if off < 0 {
		off = 0
	}
	buf, err := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	if err != nil {
		return Prompt{}
	}
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		var l transcriptLine
		if json.Unmarshal(lines[i], &l) != nil {
			continue
		}
		if s := l.prompt(); s != "" {
			return Prompt{Text: s, At: l.Timestamp.Local()}
		}
	}
	return Prompt{}
}

type Activity struct {
	ModTime time.Time
	Last    Prompt
}

var activityCache sync.Map

func LastActivity(path string) (Activity, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return Activity{}, false
	}
	key := path + "@" + st.ModTime().String()
	if v, ok := activityCache.Load(key); ok {
		return v.(Activity), true
	}
	a := Activity{ModTime: st.ModTime(), Last: LastPrompt(path)}
	activityCache.Store(key, a)
	return a, true
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
	var role, text string
	switch {
	case l.Type == "user" || l.Type == "assistant":
		role = l.Type
		if len(l.Message.Content) > 0 && l.Message.Content[0] == '"' {
			json.Unmarshal(l.Message.Content, &text)
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			json.Unmarshal(l.Message.Content, &blocks)
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, b.Text)
				}
			}
			text = strings.Join(parts, " ")
		}
	case l.Type == "response_item" && l.Payload.Type == "message":
		role = l.Payload.Role
		for _, c := range l.Payload.Content {
			text += c.Text + " "
		}
	}
	text = strings.Join(strings.Fields(Unwrap(text)), " ")
	if role == "" || text == "" || l.IsMeta || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "[") {
		return Message{}
	}
	return Message{Role: role, Text: text, Chars: utf8.RuneCountInString(text), At: l.Timestamp.Local()}
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
		for i := len(lines) - 1; i >= 0; i-- {
			if len(page.Msgs) >= n {
				drained = false
				break
			}
			b := lines[i]
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
					m.Text = m.Text[:msgTextCap] + truncMark
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
		bytes.Contains(b, []byte(`"type":"message"`)) || bytes.Contains(b, []byte(`"type":"function_call`))
}

func RecentMessages(path string, n int) []Message { return Messages(path, -1, n).Msgs }

func AllMessages(path string) []Message { return Messages(path, -1, 1<<30).Msgs }

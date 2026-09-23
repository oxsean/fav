package capture

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Pulse is what a running session's card shows, read from the transcript tail.
type Pulse struct {
	Reply   string    // the newest AI text, whitespace folded, capped
	Prompt  string    // the newest prompt, the same way
	TurnAt  time.Time // when Prompt was sent: the turn has run since then
	Context int       // tokens the last request sent (0: unknown)
	Window  int       // context window, Codex only (Claude does not record it)
	Size    int64     // file size when read: new output since the user last looked means Size grew
	ModTime time.Time
	// Asking: the last step is a question to the user (Claude AskUserQuestion / ExitPlanMode) not answered yet.
	Asking bool
	// Finished: the last thing in the file is the AI's reply (Claude text, Codex task_complete), not a tool call.
	Finished bool
}

// askTools wait for the user's answer before the turn goes on.
var askTools = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true}

const (
	pulseTail    = 512 * 1024
	pulseTextCap = 200
)

var pulseCache sync.Map // path → pulseEntry

type pulseEntry struct {
	size  int64
	mtime time.Time
	p     Pulse
}

// ReadPulse reads the last 512KB of path once per size / mtime.
func ReadPulse(path string) (Pulse, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return Pulse{}, false
	}
	if v, ok := pulseCache.Load(path); ok {
		if e := v.(pulseEntry); e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
			return e.p, true
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return Pulse{}, false
	}
	defer f.Close()
	off := max(0, st.Size()-pulseTail)
	buf, err := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	if err != nil {
		return Pulse{}, false
	}
	p := pulseOf(bytes.Split(buf, []byte{'\n'}))
	p.Size, p.ModTime = st.Size(), st.ModTime()
	pulseCache.Store(path, pulseEntry{st.Size(), st.ModTime(), p})
	return p, true
}

// pulseOf walks the lines newest first until the reply, the prompt and the context are all known; the newest meaningful line
// decides Asking and Finished.
func pulseOf(lines [][]byte) Pulse {
	var p Pulse
	haveReply, haveTurn, haveCtx, haveLast := false, false, false, false
	for i := len(lines) - 1; i >= 0 && !(haveReply && haveTurn && haveCtx && haveLast); i-- {
		var l transcriptLine
		if json.Unmarshal(lines[i], &l) != nil {
			continue
		}
		if !haveLast {
			haveLast = p.lastLine(&l)
		}
		if !haveCtx {
			switch {
			case l.Type == "assistant" && l.Message.Usage != nil:
				u := l.Message.Usage
				p.Context, haveCtx = u.Input+u.CacheCreation+u.CacheRead, true
			case l.Type == "event_msg" && l.Payload.Type == "token_count" && l.Payload.Info != nil:
				p.Context, p.Window, haveCtx = l.Payload.Info.Last.Input, l.Payload.Info.Window, true
			}
		}
		if !haveTurn {
			if s := l.prompt(); s != "" {
				p.Prompt, p.TurnAt, haveTurn = clip(strings.Join(strings.Fields(s), " "), pulseTextCap), l.Timestamp.Local(), true
			}
		}
		if !haveReply {
			if m := l.speech(); m.Role == "assistant" && strings.TrimSpace(m.Text) != "" {
				p.Reply, haveReply = clip(m.Text, pulseTextCap), true // speech already folds whitespace: one line
			}
		}
	}
	return p
}

// lastLine sets Asking / Finished from the newest line that says where the turn stands; false when l says nothing about it.
func (p *Pulse) lastLine(l *transcriptLine) bool {
	switch {
	case l.Type == "event_msg" && l.Payload.Type == "task_complete":
		p.Finished = true
		return true
	case l.Type == "event_msg" && (l.Payload.Type == "task_started" || l.Payload.Type == "user_message"):
		return true
	case l.Type == "response_item" && (l.Payload.Type == "message" || l.Payload.Type == "function_call" ||
		l.Payload.Type == "custom_tool_call" || strings.HasSuffix(l.Payload.Type, "_output")):
		return true // Codex: only task_complete says the turn is over
	case (l.Type == "assistant" || l.Type == "user") && len(l.Message.Content) > 0:
		steps := l.steps(0, false)
		if len(steps) > 0 {
			last := steps[len(steps)-1]
			p.Asking = !last.Result && askTools[last.Tool]
			return true
		}
		if m := l.speech(); m.Text != "" {
			p.Finished = m.Role == "assistant"
			return true
		}
	}
	return false
}

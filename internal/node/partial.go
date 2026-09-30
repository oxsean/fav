package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/output"
)

// partialFile holds the messages the agent is still writing (Partials), rewritten whole at most every partialEvery.
// The deltas they are made of stay out of output.log.
const (
	partialFile    = "partial.json"
	partialEvery   = 100 * time.Millisecond
	maxPartial     = 16 << 10 // a longer partial message is sent as its last maxPartial bytes
	maxPartialFile = 1 << 20
)

// Partials is what partial.json holds: no Items, nothing is being written.
type Partials struct {
	Items []Partial `json:"items"`
}

// Partial is one message the agent is writing, or a codex command still running. Key names it until its final line is
// logged; Src is the id that line carries (claude's message id, codex's item id), Kind is output.KindSay,
// output.KindThink or output.KindCmd. A command's Text is the last output.RunningTail lines of its output, Bytes all it
// printed so far.
type Partial struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Src     string `json:"src"`
	Parent  string `json:"parent,omitempty"`
	Command string `json:"command,omitempty"`
	Text    string `json:"text"`
	Bytes   int    `json:"bytes,omitempty"`
}

// partials assembles the deltas of claude's stream_event lines and codex's */delta notices into the messages being
// written and the commands running, and keeps partial.json in step: a message leaves it once its final line is logged (done), so a reader that
// sees it gone finds that line in the log.
type partials struct {
	mu    sync.Mutex
	path  string
	open  []*partial
	msgs  map[string]string // claude: the message being streamed, by parent tool call
	dirty bool
	stop  chan struct{}
	ended chan struct{}
}

type partial struct {
	Partial
	think   strings.Builder // codex reasoning: raw text, shown while no summary came
	summary strings.Builder
	part    int // codex reasoning: the summary part being written
}

func newPartials(dir string) *partials {
	p := &partials{path: filepath.Join(dir, partialFile), msgs: map[string]string{}, stop: make(chan struct{}), ended: make(chan struct{})}
	os.Remove(p.path)
	go p.loop()
	return p
}

func (p *partials) loop() {
	defer close(p.ended)
	t := time.NewTicker(partialEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.mu.Lock()
			p.flush()
			p.mu.Unlock()
		}
	}
}

// close stops the writes and removes partial.json: the agent's output ended.
func (p *partials) close() {
	close(p.stop)
	<-p.ended
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open, p.dirty = nil, true
	p.flush()
}

var (
	streamEventPattern = []byte(`"type":"stream_event"`)
	codexDeltaPattern  = []byte(`elta","params":`)
)

// take reads a line of the agent's stdout; true: it is a delta, which it took, and stays out of the log.
func (p *partials) take(line []byte) bool {
	switch {
	case bytes.Contains(line, streamEventPattern):
		return p.claude(line)
	case bytes.Contains(line, codexDeltaPattern):
		return p.codex(line)
	}
	return false
}

func (p *partials) claude(line []byte) bool {
	var l struct {
		Type   string `json:"type"`
		Parent string `json:"parent_tool_use_id"`
		Event  struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
			Block struct {
				Type string `json:"type"`
			} `json:"content_block"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		} `json:"event"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "stream_event" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	msg := p.msgs[l.Parent]
	key := msg + ":" + strconv.Itoa(l.Event.Index)
	switch l.Event.Type {
	case "message_start":
		p.msgs[l.Parent] = l.Event.Message.ID
	case "content_block_start":
		kind := map[string]string{"text": output.KindSay, "thinking": output.KindThink}[l.Event.Block.Type]
		if msg != "" && kind != "" && p.find(key) == nil {
			p.open = append(p.open, &partial{Partial: Partial{Key: key, Kind: kind, Src: msg, Parent: l.Parent}})
		}
	case "content_block_delta":
		if o := p.find(key); o != nil {
			o.Text = trimHead(o.Text + l.Event.Delta.Text + l.Event.Delta.Thinking)
			p.dirty = true
		}
	case "message_stop": // what is left of the message never got its final line
		p.drop(func(o *partial) bool { return o.Src == msg && o.Parent == l.Parent })
		delete(p.msgs, l.Parent)
	}
	return true
}

func (p *partials) codex(line []byte) bool {
	var l struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Item  string `json:"itemId"`
			Delta string `json:"delta"`
			Part  int    `json:"summaryIndex"`
		} `json:"params"`
	}
	if json.Unmarshal(line, &l) != nil || l.ID != nil || !strings.HasSuffix(strings.ToLower(l.Method), "delta") {
		return false
	}
	kind := map[string]string{"item/agentMessage/delta": output.KindSay, "item/reasoning/textDelta": output.KindThink,
		"item/reasoning/summaryTextDelta": output.KindThink, "item/commandExecution/outputDelta": output.KindCmd}[l.Method]
	if kind == "" || l.Params.Item == "" {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.find(l.Params.Item)
	if o == nil {
		o = &partial{Partial: Partial{Key: l.Params.Item, Kind: kind, Src: l.Params.Item}}
		p.open = append(p.open, o)
	}
	switch l.Method {
	case "item/reasoning/summaryTextDelta":
		if o.summary.Len() > 0 && l.Params.Part != o.part {
			o.summary.WriteString("\n")
		}
		o.part = l.Params.Part
		o.summary.WriteString(l.Params.Delta)
		o.Text = trimHead(o.summary.String())
	case "item/reasoning/textDelta":
		o.think.WriteString(l.Params.Delta)
		if o.summary.Len() == 0 {
			o.Text = trimHead(o.think.String())
		}
	case "item/commandExecution/outputDelta":
		o.Text = trimHead(cmdTail(o.Text + l.Params.Delta))
		o.Bytes += len(l.Params.Delta)
	default:
		o.Text = trimHead(o.Text + l.Params.Delta)
	}
	p.dirty = true
	return true
}

var itemStartedPattern = []byte(`"method":"item/started"`)

// done reads a line just logged: codex's start of a command lists it in partial.json; the final line of a message being
// written or a command running takes it out, and so does the end of a turn for all of them.
func (p *partials) done(line []byte) {
	var l struct {
		Type    string `json:"type"`
		Method  string `json:"method"`
		Parent  string `json:"parent_tool_use_id"`
		Message struct {
			ID      string `json:"id"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"message"`
		Params struct {
			Item struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"item"`
		} `json:"params"`
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.open) == 0 && !bytes.Contains(line, itemStartedPattern) || json.Unmarshal(line, &l) != nil {
		return
	}
	switch {
	case l.Method == "item/started":
		it := l.Params.Item
		if it.Type != "commandExecution" || it.ID == "" {
			return
		}
		if o := p.find(it.ID); o != nil {
			o.Command = it.Command
		} else {
			p.open = append(p.open, &partial{Partial: Partial{Key: it.ID, Kind: output.KindCmd, Src: it.ID, Command: it.Command}})
		}
		p.dirty = true
	case l.Type == "assistant" && len(l.Message.Content) > 0:
		kind := map[string]string{"text": output.KindSay, "thinking": output.KindThink}[l.Message.Content[0].Type]
		for _, o := range p.open {
			if o.Src == l.Message.ID && o.Kind == kind {
				p.drop(func(x *partial) bool { return x == o })
				break
			}
		}
	case l.Method == "item/completed":
		p.drop(func(o *partial) bool { return o.Src == l.Params.Item.ID })
	case l.Type == "result", l.Method == "turn/completed":
		p.drop(func(*partial) bool { return true })
	default:
		return
	}
	p.flush()
}

func (p *partials) find(key string) *partial {
	for _, o := range p.open {
		if o.Key == key {
			return o
		}
	}
	return nil
}

// drop takes out the messages gone says; the caller holds mu.
func (p *partials) drop(gone func(*partial) bool) {
	kept := p.open[:0]
	for _, o := range p.open {
		if gone(o) {
			p.dirty = true
		} else {
			kept = append(kept, o)
		}
	}
	clear(p.open[len(kept):])
	p.open = kept
}

// flush writes partial.json when it changed, or removes it when nothing is being written; the caller holds mu.
func (p *partials) flush() {
	if !p.dirty {
		return
	}
	p.dirty = false
	var ps Partials
	for _, o := range p.open {
		if o.Text != "" || o.Kind == output.KindCmd {
			ps.Items = append(ps.Items, o.Partial)
		}
	}
	if len(ps.Items) == 0 {
		os.Remove(p.path)
		return
	}
	for i := range ps.Items {
		ps.Items[i].Text = sentPartial(ps.Items[i].Text)
	}
	b, _ := json.Marshal(ps)
	fileio.WriteFile(p.path, b, 0o600)
}

// cmdTail is the last output.RunningTail lines of a command's output, the line being written counted as one.
func cmdTail(s string) string {
	end := strings.TrimSuffix(s, "\n")
	i := len(end)
	for range output.RunningTail {
		if i = strings.LastIndexByte(end[:i], '\n'); i < 0 {
			return s
		}
	}
	return s[i+1:]
}

// trimHead keeps a message being assembled to twice what is sent of it.
func trimHead(s string) string {
	if len(s) <= 2*maxPartial {
		return s
	}
	return "…" + runeTail(s, maxPartial)
}

// sentPartial is s as partial.json holds it: the last maxPartial bytes of a longer one, after "…".
func sentPartial(s string) string {
	if len(s) <= maxPartial {
		return s
	}
	return "…" + runeTail(s, maxPartial)
}

func runeTail(s string, n int) string {
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// readPartials reads the run's partial.json as it is now: nil when there is none. ok false: it could not be read this
// time (a Windows rename in the way), which the next look tries again.
func readPartials(dir string) (raw []byte, ok bool) {
	f, err := os.Open(filepath.Join(dir, partialFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, true
	}
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err = io.ReadAll(io.LimitReader(f, maxPartialFile))
	if err != nil || !json.Valid(raw) {
		return nil, false
	}
	return raw, true
}

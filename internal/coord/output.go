package coord

import (
	"context"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

type OutputPageParams struct {
	Run    string `json:"run"`
	Before int64  `json:"before"` // < 0: from the end
	N      int    `json:"n,omitempty"`
	Raw    bool   `json:"raw,omitempty"`
	File   string `json:"file,omitempty"`
}

// OutputPage is a stretch of a run's output as events: From is where its first line starts, which the page before
// ends at; To where its last whole line ends; Earliest the first position still to be read. Raw is the stretch's text
// as written, when asked for.
type OutputPage struct {
	Events   []output.Event `json:"events"`
	From     int64          `json:"from"`
	To       int64          `json:"to"`
	Earliest int64          `json:"earliest"`
	File     string         `json:"file,omitempty"`
	Prev     string         `json:"prev,omitempty"` // the page starts the current log: the log before it, still kept
	Turn     int            `json:"turn,omitempty"` // the turn the page starts in, when the node's marks know it
	Raw      string         `json:"raw,omitempty"`
}

// A page reads the node's log outputChunk first, twice as much each time after, until it holds its events or
// outputMax.
var outputChunk = 64 << 10

const (
	outputMax       = 1 << 20
	outputEvents    = 200
	maxOutputEvents = 1000
)

func (c *Coord) outputPage(ctx context.Context, p Principal, r *wire.Request) (any, error) {
	var op OutputPageParams
	if err := r.Decode(&op); err != nil {
		return nil, err
	}
	run, err := c.readableRun(p, op.Run)
	if err != nil {
		return nil, err
	}
	n := op.N
	if n <= 0 {
		n = outputEvents
	}
	n = min(n, maxOutputEvents)
	var (
		lines []node.TailLine
		from  int64
		file  = op.File
		prev  string
		st    *output.State
		evs   []output.Event
		to    int64
		size  int
	)
	for before, chunk := op.Before, outputChunk; ; chunk *= 2 {
		var t node.Tail
		if err := c.call(ctx, run.Machine, node.MRunTail, node.TailParams{Run: op.Run, Before: before, Max: chunk, File: file, Clip: !op.Raw}, &t); err != nil {
			return nil, err
		}
		got := t.Lines
		if got == nil { // raw, or a node that sends only text
			got = textLines(t.Text, t.From)
		}
		for _, l := range got {
			size += len(l.Text)
		}
		stuck := before >= 0 && t.From >= before
		lines, from, file, prev, st = append(got, lines...), t.From, t.File, t.Prev, t.Turn
		evs, to = eventsOf(file, lines, st)
		if whole(evs) >= n || t.Done || size >= outputMax || stuck {
			break
		}
		before = t.From
	}
	if extra := whole(evs) - n; extra > 0 { // the last n, and the rest of the first one's line
		k := 0
		for ; extra > 0; k++ {
			if !evs[k].Temp {
				extra--
			}
		}
		for k > 0 && evs[k-1].Off == evs[k].Off {
			k--
		}
		evs = evs[k:]
	}
	pageFrom := from
	if len(evs) > 0 && !evs[0].Temp {
		pageFrom = evs[0].Off
	}
	page := OutputPage{Events: evs, From: pageFrom, To: max(to, pageFrom), File: file}
	if pageFrom == 0 {
		page.Prev = prev
	}
	if st == nil {
		for i := range evs {
			evs[i].Turn = 0 // ⚠️ without the node's marks a page cannot know its turns
		}
	} else if len(evs) > 0 {
		page.Turn = evs[0].Turn
	}
	if op.Raw {
		var b strings.Builder
		for _, l := range lines {
			if l.Off >= pageFrom {
				b.WriteString(l.Text)
			}
		}
		page.Raw = b.String()
	}
	return page, nil
}

// textLines is text, read from off, as a page's lines.
func textLines(text string, off int64) []node.TailLine {
	var out []node.TailLine
	for text != "" {
		line, rest, whole := strings.Cut(text, "\n")
		if whole {
			line += "\n"
		}
		out = append(out, node.TailLine{Off: off, Text: line})
		off, text = off+int64(len(line)), rest
	}
	return out
}

// eventsOf reads a page's lines from st on (nil: unknown, read as the first turn); it answers the events and where
// the last whole line ends. A line the node cut is marked truncated, and one sent as only its head is raw.
func eventsOf(file string, lines []node.TailLine, st *output.State) ([]output.Event, int64) {
	var s output.State
	if st != nil {
		s = *st
	}
	var evs []output.Event
	var to int64
	if len(lines) > 0 {
		to = lines[0].Off
	}
	for _, l := range lines {
		if l.Head {
			evs = append(evs, output.Event{ID: file + ":" + strconv.FormatInt(l.Off, 10) + ":0", Off: l.Off, Kind: output.KindRaw,
				Text: l.Text, Turn: max(s.Turn, 1), Truncated: map[string]int{"line": int(l.Size)}})
			to = l.Off + l.Size
			continue
		}
		e, next, end := output.Parse(file, l.Off, l.Text, s)
		if l.Size > 0 {
			for i := range e {
				if e[i].Truncated == nil {
					e[i].Truncated = map[string]int{}
				}
				e[i].Truncated["line"] = int(l.Size)
			}
			if end > l.Off {
				end = l.Off + l.Size
			}
		}
		if end > l.Off {
			to = end
		}
		s, evs = next, append(evs, e...)
	}
	return evs, to
}

func whole(evs []output.Event) int {
	n := 0
	for _, e := range evs {
		if !e.Temp {
			n++
		}
	}
	return n
}

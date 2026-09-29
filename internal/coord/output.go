package coord

import (
	"context"

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
		text string
		from int64
		file = op.File
		evs  []output.Event
		to   int64
	)
	for before, chunk := op.Before, outputChunk; ; chunk *= 2 {
		var t node.Tail
		if err := c.call(ctx, run.Machine, node.MRunTail, node.TailParams{Run: op.Run, Before: before, Max: chunk, File: file}, &t); err != nil {
			return nil, err
		}
		text, from, file = t.Text+text, t.From, t.File
		evs, _, to = output.Parse(file, from, text, output.State{})
		if whole(evs) >= n || t.Done || len(text) >= outputMax {
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
	for i := range evs {
		evs[i].Turn = 0 // ⚠️ a page is read from State{}: the page's turn comes with marks.jsonl
	}
	page := OutputPage{Events: evs, From: pageFrom, To: max(to, pageFrom), File: file}
	if op.Raw {
		page.Raw = text[pageFrom-from:]
	}
	return page, nil
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
